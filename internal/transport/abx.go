package transport

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// abxTransport drives a web target via the abx CLI.
type abxTransport struct{ bin string }

// NewABX returns the abx transport.
func NewABX() Transport { return &abxTransport{bin: "abx"} }

func (a *abxTransport) Name() string { return "abx" }

func (a *abxTransport) Capabilities() Caps {
	return Caps{Web: true, Screenshot: true, GUI: true}
}

func (a *abxTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(a.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH", a.bin)}
	}
	res, err := runExternal(ctx, a.bin, []string{"--help"}, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: "abx ok"}
}

// Run executes the argv on the local host. abx targets are macOS-local
// (Playwright Chromium runs here); shell steps land in the user's local
// shell instead of being misforwarded as abx subcommands. Use `gui:` for
// browser actions, or invoke `abx` directly via run.
func (a *abxTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{ExitCode: 0}, nil
	}
	if isABXVerb(cmd[0]) {
		return a.runABX(ctx, t, cmd, stdout, stderr)
	}
	return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
}

// RunWeb forwards argv to abx unconditionally. `vmlab web` uses this instead
// of Run so an unknown verb errors loudly inside abx (exit 1) rather than
// silently executing a same-named local binary — `vmlab web t -- open url`
// must never reach /usr/bin/open.
func (a *abxTransport) RunWeb(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{ExitCode: 0}, nil
	}
	return a.runABX(ctx, t, cmd, stdout, stderr)
}

func (a *abxTransport) Sync(ctx context.Context, t target.Target, src string) error { return nil }

func (a *abxTransport) Shell(ctx context.Context, t target.Target) error {
	return shellInteractive(ctx, a.bin, []string{"shell"})
}

func (a *abxTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	res, err := a.runABX(ctx, t, []string{"screenshot", path}, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("abx screenshot exited %d", res.ExitCode)
	}
	return nil
}

// GUI dispatches a structured gui: step to the matching abx verb. This is
// the TCC-free path for web screenshots and any browser-driven E2E — abx
// captures pixels from its own Playwright-controlled Chromium, so the
// macOS Screen Recording grant is irrelevant. For native macOS apps use
// the guiport transport instead.
//
// Kinds covered:
//   - screenshot — abx screenshot [path]
//   - click      — abx click <selector>
//   - click-text — abx click "text=<value>" (Playwright's text= engine)
//   - type       — abx type <text> (uses currently focused element)
//   - hotkey     — abx press <key>
//   - wait       — abx wait <selector> when Selector is set, else sleep
//   - observe    — abx accessibility
//   - tree       — abx snapshot
//   - open-url   — abx goto <url>
//   - run        — abx <args from Path> (raw forwarding for advanced cases)
func (a *abxTransport) GUI(ctx context.Context, t target.Target, action GUIAction, stdout, stderr io.Writer) error {
	var verb []string
	switch action.Kind {
	case "screenshot":
		path := action.Path
		if path == "" {
			return fmt.Errorf("abx screenshot requires path")
		}
		verb = []string{"screenshot", path}
	case "click":
		if action.Selector == "" {
			return fmt.Errorf("abx click requires selector")
		}
		verb = []string{"click", action.Selector}
	case "click-text":
		if action.Text == "" {
			return fmt.Errorf("abx click-text requires text")
		}
		// Playwright's `text=` engine accepts a substring match by default.
		verb = []string{"click", "text=" + action.Text}
	case "type":
		if action.Text == "" {
			return fmt.Errorf("abx type requires text")
		}
		verb = []string{"type", action.Text}
	case "hotkey":
		key := action.Text
		if key == "" {
			key = action.Selector
		}
		if key == "" {
			return fmt.Errorf("abx hotkey requires text (key combo)")
		}
		verb = []string{"press", key}
	case "wait":
		if action.Selector != "" {
			verb = []string{"wait", action.Selector}
		} else {
			ms := extraInt(action.Extra, "milliseconds")
			if ms == 0 {
				ms = extraInt(action.Extra, "ms")
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
		}
	case "observe":
		verb = []string{"accessibility"}
	case "tree":
		verb = []string{"snapshot"}
	case "open-url":
		url := action.Path
		if url == "" {
			url = action.Text
		}
		if url == "" {
			return fmt.Errorf("abx open-url requires path or text (the URL)")
		}
		verb = []string{"goto", url}
	default:
		return fmt.Errorf("abx: unsupported gui kind %q", action.Kind)
	}
	res, err := a.runABX(ctx, t, verb, stdout, stderr)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("abx %s exited %d", action.Kind, res.ExitCode)
	}
	return nil
}

func (a *abxTransport) runABX(ctx context.Context, t target.Target, args []string, stdout, stderr io.Writer) (Result, error) {
	return runExternalEnv(ctx, a.bin, abxArgs(t, args), []string{"BROWSE_PARENT_PID=0"}, stdout, stderr)
}

// abxArgs assembles the per-invocation prefix for an abx call. `abx.mode:
// live` wraps every command as `abx live <verb>` to drive a CDP Chrome target.
// Prefer the dedicated chrome-agent profile over the user's personal Chrome.
// The persistent abx server keeps URL state across calls, so we don't pass
// --url here — emit a goto step first or use the per-action URL fields.
func abxArgs(t target.Target, extra []string) []string {
	var args []string
	if mode := t.SettingString("abx", "mode"); mode == "live" {
		args = append(args, "live")
	}
	args = append(args, extra...)
	return args
}

// isABXVerb routes flow `run:` steps on abx targets: known verbs go to abx,
// anything else lands in the local shell. Keep in sync with `abx --help`
// (the list abx prints on an unknown command is authoritative). `vmlab web`
// bypasses this entirely via RunWeb, so a stale entry here only affects flows.
func isABXVerb(verb string) bool {
	switch verb {
	case "goto", "back", "forward", "reload", "url",
		"text", "html", "links", "forms", "accessibility",
		"click", "fill", "select", "hover", "type", "press", "scroll", "wait", "viewport", "upload",
		"focus", "frame", "load-html",
		"cookie-import", "cookie-import-browser",
		"js", "eval", "css", "attrs", "console", "network", "dialog", "cookies", "storage", "perf", "is",
		"inspect", "state", "style", "media", "data",
		"screenshot", "pdf", "responsive", "prettyscreenshot", "ux-audit",
		"snapshot", "diff", "scrape", "archive", "download", "watch",
		"live", "chain", "cdp", "connect", "disconnect",
		"tabs", "tab", "newtab", "closetab", "tab-each",
		"status", "cookie", "header", "useragent", "stop", "restart", "cleanup", "resume",
		"skill", "domain-skill", "handoff", "inbox",
		"dialog-accept", "dialog-dismiss":
		return true
	default:
		return false
	}
}
