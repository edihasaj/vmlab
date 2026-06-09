package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// guiportTransport drives a desktop UI via the guiport CLI.
type guiportTransport struct{ bin string }

// NewGuiport returns the guiport transport.
func NewGuiport() Transport { return &guiportTransport{bin: "guiport"} }

func (g *guiportTransport) Name() string { return "guiport" }

func (g *guiportTransport) Capabilities() Caps {
	return Caps{GUI: true, Screenshot: true}
}

func (g *guiportTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(g.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH", g.bin)}
	}
	out, _ := captureOutput(ctx, g.bin, []string{"doctor"})
	axOK := !strings.Contains(out, "accessibility: not trusted")
	srOK := !strings.Contains(out, "screen_recording: not granted")
	if axOK && srOK {
		return Health{OK: true, Message: "guiport doctor ok"}
	}
	// `guiport doctor` runs as a CLI, so its Screen Recording probe reflects
	// the *terminal's* grant, not guiport.app's. When the app bundle is present
	// capture routes through it (its own SR grant), so a CLI-level SR miss is
	// not fatal as long as Accessibility is granted.
	if !srOK && axOK {
		if app := guiportAppBundle(t); app != "" {
			return Health{OK: true, Message: "guiport ok (AX granted; screenshots via guiport.app)"}
		}
		return Health{OK: false, Message: "guiport: Screen Recording not granted — install guiport.app or grant the terminal (run: vmlab grant guiport screen-recording)"}
	}
	return Health{OK: false, Message: "guiport: Accessibility not trusted (run: vmlab grant guiport accessibility)"}
}

// appFlags returns the per-subcommand flags this target wants applied to
// guiport verbs that accept them (click, click-text, screenshot, observe,
// tree). `--app` and `--strict` are subcommand-scoped in guiport's CLI, not
// global — so they must trail the verb, not precede it.
func appFlags(t target.Target) []string {
	var args []string
	if app := t.SettingString("guiport", "app"); app != "" {
		args = append(args, "--app", app)
	}
	if t.Setting("guiport", "strict") == true {
		args = append(args, "--strict")
	}
	return args
}

// Run executes the argv on the local host. Guiport targets are always the
// machine vmlab is running on, so a flow that mixes `run:` (shell) and
// `gui:` (UI) steps Just Works — the shell steps land in the user's local
// shell instead of being misforwarded as guiport subcommands. Use the
// structured GUI() method for desktop actions.
func (g *guiportTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{ExitCode: 0}, nil
	}
	return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
}

func (g *guiportTransport) Sync(ctx context.Context, t target.Target, src string) error { return nil }

func (g *guiportTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("guiport: shell not supported")
}

func (g *guiportTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	return g.capture(ctx, t, path)
}

// capture writes a screenshot to path.
//
// macOS attributes Screen Recording to the *responsible* process, which for a
// CLI launched from a shell is the terminal — not guiport. So a bare `guiport
// screenshot` only works if the launching terminal itself holds the SR grant
// (fragile, and impossible under a detached tmux/agent). When the guiport.app
// bundle is installed we route capture through `open`, making guiport its own
// responsible process so the SR grant lands on guiport.app and persists. The
// direct-binary path stays as a fallback for hosts without the bundle.
func (g *guiportTransport) capture(ctx context.Context, t target.Target, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	extra := appFlags(t)
	if app := guiportAppBundle(t); app != "" {
		// -n forces a fresh instance (no stale resident process); -W waits for
		// it to exit. The app's CWD is `/`, so the output path must be absolute.
		args := append([]string{"-n", "-W", "-a", app, "--args", "screenshot", "--out", abs}, extra...)
		if _, err := runExternal(ctx, "open", args, io.Discard, io.Discard); err != nil {
			return err
		}
		if fi, statErr := os.Stat(abs); statErr == nil && fi.Size() > 0 {
			return nil
		}
		return fmt.Errorf("guiport screenshot via %s produced no output — grant Screen Recording to guiport.app (System Settings → Privacy & Security → Screen Recording)", app)
	}
	args := append([]string{"screenshot", "--out", abs}, extra...)
	res, err := runExternal(ctx, g.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("guiport screenshot exited %d", res.ExitCode)
	}
	return nil
}

// guiportAppBundle returns the path to a guiport.app bundle, or "" if none is
// installed. Resolution order: the VMLAB_GUIPORT_APP env var (a path, or
// "off"/"none"/"0" to force the direct-binary path), then the
// `guiport.appBundle` target setting, then the well-known locations.
func guiportAppBundle(t target.Target) string {
	switch v := os.Getenv("VMLAB_GUIPORT_APP"); v {
	case "off", "none", "0":
		return ""
	case "":
		// fall through to discovery
	default:
		if fi, err := os.Stat(v); err == nil && fi.IsDir() {
			return v
		}
	}
	candidates := []string{
		t.SettingString("guiport", "appBundle"),
		"/Applications/guiport.app",
		filepath.Join(os.Getenv("HOME"), "Applications", "guiport.app"),
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

func (g *guiportTransport) GUI(ctx context.Context, t target.Target, a GUIAction, stdout, stderr io.Writer) error {
	var args []string
	app := appFlags(t)
	switch a.Kind {
	case "click":
		args = append([]string{"click", a.Selector}, app...)
	case "click-text":
		args = append([]string{"click-text", a.Text}, app...)
	case "click-at":
		// click-at uses raw screen coords; no --app on this verb.
		args = []string{"click-at", fmt.Sprintf("%d", extraInt(a.Extra, "x")), fmt.Sprintf("%d", extraInt(a.Extra, "y"))}
	case "type":
		// guiport type takes the text positionally and operates on the
		// focused element — no --into selector. Use a prior `click:` step
		// to focus the right field.
		args = []string{"type", a.Text}
	case "hotkey":
		chord := a.Text
		if chord == "" {
			chord = a.Selector
		}
		args = []string{"hotkey", chord}
	case "screenshot":
		// Route through the app bundle when present so the SR grant applies.
		return g.capture(ctx, t, a.Path)
	case "observe":
		args = append([]string{"observe"}, app...)
	case "tree":
		args = append([]string{"tree"}, app...)
	case "wait":
		// Guiport has no `wait` verb — implement it transport-side so flows
		// can pause between UI steps without dropping back to a shell sleep.
		ms := extraInt(a.Extra, "milliseconds")
		if ms == 0 {
			ms = extraInt(a.Extra, "ms")
		}
		if ms < 0 {
			ms = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(ms) * time.Millisecond):
		}
		return nil
	case "run", "run-flow":
		args = []string{"run", a.Path}
	case "approve":
		return g.approve(ctx, t, a)
	// Lifecycle (guiport ≥0.2). `--app` comes from target settings.
	case "launch":
		args = append([]string{"lifecycle", "launch"}, app...)
	case "quit":
		args = append([]string{"lifecycle", "quit"}, app...)
	case "kill":
		args = append([]string{"lifecycle", "kill"}, app...)
	case "restart":
		args = append([]string{"lifecycle", "restart"}, app...)
	// Logs — no --app; expects extras like process/subsystem/category/last.
	case "logs":
		args = []string{"logs"}
	// FS via Finder — flags come from Extra (src/into/path/to).
	case "fs-create":
		args = []string{"fs", "create"}
	case "fs-rename":
		args = []string{"fs", "rename"}
	case "fs-trash":
		args = []string{"fs", "trash"}
	case "fs-reveal":
		args = []string{"fs", "reveal"}
	default:
		return fmt.Errorf("guiport: unknown action kind %q", a.Kind)
	}
	// `extra` may carry pass-through flags. We already consumed the keys we
	// know about (x/y/milliseconds/ms); pipe the rest in as --k v.
	for k, v := range a.Extra {
		switch k {
		case "x", "y", "milliseconds", "ms":
			continue
		}
		args = append(args, "--"+k, fmt.Sprintf("%v", v))
	}
	res, err := runExternal(ctx, g.bin, args, stdout, stderr)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		// surface failure with the action for context
		b, _ := json.Marshal(a)
		return fmt.Errorf("guiport %s exited %d (action=%s)", a.Kind, res.ExitCode, string(b))
	}
	return nil
}

// approve polls for a consent dialog and clicks the first matching button.
// `deny` is checked before `allow` so callers can short-circuit on a refuse
// label (e.g. "Don't Send"). Labels are matched via guiport click-text, which
// is case-insensitive substring on the visible text of clickable elements;
// a non-zero exit from click-text means "no such target yet" and we keep
// polling.
//
// The default allow list covers the buttons most consent dialogs ship with
// on macOS (NSAlert + Catalyst + Electron-style). Callers can override via
// extra.allow / extra.deny. extra.timeout overrides the default 10s.
//
// Limitations:
//   - System TCC prompts (Touch ID/password sheets) live outside AX and
//     cannot be approved here — that's the one human step we accept.
//   - Multiple dialogs stacked at once: we click the first match we find,
//     not necessarily the topmost. Sequence approve steps if order matters.
func (g *guiportTransport) approve(ctx context.Context, t target.Target, a GUIAction) error {
	allow := extraStringSlice(a.Extra, "allow")
	if len(allow) == 0 {
		allow = []string{"Allow", "OK", "Continue", "Yes", "Trust", "Open", "Always Allow", "Allow While Using App", "Allow Once"}
	}
	deny := extraStringSlice(a.Extra, "deny")

	timeout := 10 * time.Second
	if s := extraString(a.Extra, "timeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			timeout = d
		}
	}

	deadline := time.Now().Add(timeout)
	delay := 250 * time.Millisecond
	app := appFlags(t)
	for {
		// Deny first so an explicit refuse pre-empts a generic allow.
		for _, label := range deny {
			if g.tryClickText(ctx, label, app) {
				return nil
			}
		}
		for _, label := range allow {
			if g.tryClickText(ctx, label, app) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guiport approve: no matching dialog within %s (allow=%v deny=%v)", timeout, allow, deny)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// tryClickText runs `guiport click-text <label>` and reports whether it
// succeeded. Output is suppressed so the polling loop stays quiet on misses.
func (g *guiportTransport) tryClickText(ctx context.Context, label string, app []string) bool {
	args := append([]string{"click-text", label}, app...)
	var buf bytes.Buffer
	res, err := runExternal(ctx, g.bin, args, &buf, &buf)
	if err != nil || res.ExitCode != 0 {
		return false
	}
	// Some guiport builds exit 0 with a "no match" line on stderr. Treat
	// that as a miss so we don't claim a click that never happened.
	out := strings.ToLower(buf.String())
	if strings.Contains(out, "no match") || strings.Contains(out, "not found") {
		return false
	}
	return true
}

func extraStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		// Allow comma-separated for terse YAML.
		parts := strings.Split(x, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func extraString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// extraInt reads an integer-ish value from the Extra map, accepting int /
// int64 / float64 / string forms. Returns 0 when the key is absent or
// unparseable — kinds that need a real value should range-check separately.
func extraInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var i int
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}
