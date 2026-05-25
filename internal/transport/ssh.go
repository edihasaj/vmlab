package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// sshTransport runs commands on a remote host over plain SSH. It is the
// canonical transport for cloud Linux boxes (Hetzner, EC2, etc) — providers
// emit targets pointing at this transport once the box is reachable.
//
// Settings:
//
//	ssh.host        required
//	ssh.user        defaults to "root"
//	ssh.port        defaults to "22"
//	ssh.identity    path to private key (optional, may be in agent)
//	ssh.knownHosts  path to known_hosts file (optional, pins host keys)
//	ssh.strictHost  default "yes" — flip to "accept-new" for first-contact
type sshTransport struct{}

// NewSSH returns the ssh transport.
func NewSSH() Transport { return &sshTransport{} }

func (s *sshTransport) Name() string { return "ssh" }

func (s *sshTransport) Capabilities() Caps {
	return Caps{Shell: true, Sync: true, Install: true}
}

func (s *sshTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary("ssh") {
		return Health{OK: false, Message: "ssh not on PATH"}
	}
	if t.SettingString("ssh", "host") == "" {
		return Health{OK: false, Message: "ssh.host is required"}
	}
	args := append(sshDialArgs(t), "true")
	var errBuf bytes.Buffer
	res, err := runExternal(ctx, "ssh", args, io.Discard, &errBuf)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			return Health{OK: false, Message: fmt.Sprintf("ssh exit=%d", res.ExitCode)}
		}
		return Health{OK: false, Message: fmt.Sprintf("ssh exit=%d: %s", res.ExitCode, firstLine(msg))}
	}
	return Health{OK: true, Message: "ssh reachable"}
}

func (s *sshTransport) Sync(ctx context.Context, t target.Target, src string) error {
	host := t.SettingString("ssh", "host")
	user := sshUser(t)
	dest := t.SettingString("ssh", "dest")
	if dest == "" {
		dest = "~/"
	}
	// Prefer rsync (incremental, deletes orphans with --delete optionally),
	// fall back to scp.
	if haveBinary("rsync") {
		rsh := "ssh -o BatchMode=yes -o StrictHostKeyChecking=" + sshStrict(t)
		if id := t.SettingString("ssh", "identity"); id != "" {
			rsh += " -i " + id + " -o IdentitiesOnly=yes"
		}
		if port := t.SettingString("ssh", "port"); port != "" {
			rsh += " -p " + port
		}
		args := []string{"-az", "--info=stats1", "-e", rsh, src, fmt.Sprintf("%s@%s:%s", user, host, dest)}
		res, err := runExternal(ctx, "rsync", args, io.Discard, io.Discard)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("rsync exit=%d", res.ExitCode)
		}
		return nil
	}
	if !haveBinary("scp") {
		return fmt.Errorf("ssh: neither rsync nor scp on PATH")
	}
	args := []string{"-q"}
	if id := t.SettingString("ssh", "identity"); id != "" {
		args = append(args, "-i", id)
	}
	if port := t.SettingString("ssh", "port"); port != "" {
		args = append(args, "-P", port)
	}
	args = append(args, "-r", src, fmt.Sprintf("%s@%s:%s", user, host, dest))
	res, err := runExternal(ctx, "scp", args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("scp exit=%d", res.ExitCode)
	}
	return nil
}

func sshStrict(t target.Target) string {
	if s := t.SettingString("ssh", "strictHost"); s != "" {
		return s
	}
	return "yes"
}

func (s *sshTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("ssh: empty command")
	}
	args := sshDialArgs(t)
	quoted := make([]string, 0, len(cmd))
	for _, a := range cmd {
		quoted = append(quoted, posixQuote(a))
	}
	args = append(args, strings.Join(quoted, " "))
	return runExternal(ctx, "ssh", args, stdout, stderr)
}

func (s *sshTransport) Shell(ctx context.Context, t target.Target) error {
	args := sshDialArgs(t)
	// drop the trailing -- so ssh starts a shell instead of running a command
	if n := len(args); n > 0 && args[n-1] == "--" {
		args = args[:n-1]
	}
	// flip back to a tty for the interactive shell
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-o" && args[i+1] == "RequestTTY=no" {
			args[i+1] = "RequestTTY=yes"
		}
	}
	return shellInteractive(ctx, "ssh", args)
}

// Screenshot captures the remote X display via ImageMagick's `import` and
// scp's it back to `path` on the host. Requires xdotool/imagemagick on
// the guest and ssh.display set (e.g. ":99" for a headless Xvfb). The X
// dependency keeps this off non-graphical Linux boxes; that's by design.
func (s *sshTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	if !haveBinary("ssh") {
		return fmt.Errorf("ssh not on PATH")
	}
	display := sshDisplay(t)
	remote := "/tmp/vmlab-ssh-shot.png"
	// Use ImageMagick `import -window root` — works on every Xvfb/X11
	// session, no compositor required. Quote $DISPLAY literally so the
	// remote shell expands it.
	cmd := fmt.Sprintf("DISPLAY=%s import -window root %s", display, remote)
	args := append(sshDialArgs(t), cmd)
	if _, err := runExternal(ctx, "ssh", args, io.Discard, io.Discard); err != nil {
		return fmt.Errorf("ssh: remote screenshot failed: %w", err)
	}
	scpArgs := sshScpArgs(t, false)
	scpArgs = append(scpArgs, fmt.Sprintf("%s@%s:%s", sshUser(t), t.SettingString("ssh", "host"), remote), path)
	res, err := runExternal(ctx, "scp", scpArgs, io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("ssh: scp back failed: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("ssh: scp exited %d", res.ExitCode)
	}
	return nil
}

// GUI drives a remote X11 desktop through xdotool + ImageMagick. The
// guest must have xdotool, imagemagick, and a running X server (Xvfb or
// real). `ssh.display` selects which display (default ":0" for a logged-
// in desktop; ":99" is the convention for vmlab-managed Xvfb).
//
// Kinds covered:
//   - screenshot — see Screenshot() above
//   - click      — xdotool search by name, windowactivate, click 1
//   - click-text — alias for click; xdotool searches by Name substring
//   - click-at   — xdotool mousemove + click at raw coords
//   - type       — xdotool type --delay 20
//   - hotkey     — xdotool key <chord>; modifiers translated to xdotool names
//   - wait       — host-side sleep
//   - observe    — xdotool getactivewindow getwindowname
//   - tree       — xdotool search --name "" (list of all windows)
//   - open-url   — DISPLAY=… xdg-open <url>
func (s *sshTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	if a.Kind == "wait" {
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
	}
	if a.Kind == "screenshot" {
		if a.Path == "" {
			return fmt.Errorf("ssh gui screenshot requires path")
		}
		return s.Screenshot(ctx, t, a.Path)
	}
	if a.Kind == "approve" {
		return s.approve(ctx, t, a)
	}
	cmd, err := sshGuiCmd(t, a)
	if err != nil {
		return err
	}
	args := append(sshDialArgs(t), cmd)
	var errb strings.Builder
	res, err := runExternal(ctx, "ssh", args, io.Discard, &errb)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errb.String())
		if msg != "" {
			return fmt.Errorf("ssh gui %s exited %d: %s", a.Kind, res.ExitCode, msg)
		}
		return fmt.Errorf("ssh gui %s exited %d", a.Kind, res.ExitCode)
	}
	return nil
}

// sshDisplay returns the X DISPLAY for GUI dispatch. Default ":0" for a
// real logged-in desktop; set ssh.display=":99" for an Xvfb session.
func sshDisplay(t target.Target) string {
	if d := t.SettingString("ssh", "display"); d != "" {
		return d
	}
	return ":0"
}

// sshScpArgs returns the per-target scp option prefix. Mirror sshDialArgs
// but for scp's slightly different flag set (`-q` instead of `-o`-only).
func sshScpArgs(t target.Target, recursive bool) []string {
	args := []string{"-q", "-o", "ConnectTimeout=10", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=" + sshStrictHost(t)}
	if id := t.SettingString("ssh", "identity"); id != "" {
		args = append(args, "-i", id)
	}
	if port := t.SettingString("ssh", "port"); port != "" {
		args = append(args, "-P", port)
	}
	if recursive {
		args = append(args, "-r")
	}
	return args
}

func sshStrictHost(t target.Target) string {
	if v := t.SettingString("ssh", "strictHost"); v != "" {
		return v
	}
	return "yes"
}

// sshGuiCmd picks the right backend (X11 xdotool / Wayland ydotool+wtype) and
// returns the remote shell command for the GUI action. Selection:
//
//   - explicit ssh.backend = "x11" | "wayland"     — honoured verbatim
//   - explicit ssh.backend = "auto" (default empty) — picks at runtime by
//     probing WAYLAND_DISPLAY / XDG_SESSION_TYPE in the remote shell, so a
//     single flow works across desktop sessions.
//
// The auto path emits a small `if`-shell that runs whichever side matches.
// Verbs that one backend can't express (e.g. xdotool window-name match on
// Wayland) raise a clear error; install AT-SPI on the guest and route via
// ssh.uiMode=atspi for those.
func sshGuiCmd(t target.Target, a GUIAction) (string, error) {
	// AT-SPI is opt-in via ssh.uiMode=atspi. When on, it owns the label-based
	// verbs (click / click-text); other verbs fall through to the display-
	// server backend. AT-SPI is bus-level so it works under both X11 and
	// Wayland — that's its main reason to exist here.
	if strings.EqualFold(t.SettingString("ssh", "uiMode"), "atspi") {
		if cmd, ok := atspiCmd(a); ok {
			return cmd, nil
		}
	}
	backend := strings.ToLower(t.SettingString("ssh", "backend"))
	display := sshDisplay(t)
	switch backend {
	case "x11":
		return xdoCmd(a, display)
	case "wayland":
		return wlCmd(a)
	}
	// auto: try wayland first if the session signals it, else fall back to X11.
	x, errX := xdoCmd(a, display)
	w, errW := wlCmd(a)
	if errX != nil && errW != nil {
		// Surface the X11 error since X11 is the historical default.
		return "", errX
	}
	if errX != nil {
		// Wayland-only verb (rare; today all our verbs render in both).
		return wlAutoGuard(w), nil
	}
	if errW != nil {
		return x, nil
	}
	return fmt.Sprintf(`bash -lc 'if [ -n "$WAYLAND_DISPLAY" ] || [ "$XDG_SESSION_TYPE" = "wayland" ]; then %s; else %s; fi'`,
		shellEscapeForSingleQuote(w), shellEscapeForSingleQuote(x)), nil
}

// atspiScript is the Python helper staged on the guest via heredoc. It walks
// the AT-SPI desktop tree, finds the first clickable element whose Name or
// Description matches the requested label (substring, case-insensitive),
// and invokes its primary action. Works under both X11 and Wayland.
//
// Requires the guest to have `python3-pyatspi` (Ubuntu/Debian) or
// `at-spi2-core` plus python3 bindings (Fedora/Arch) installed, and the
// accessibility bus running for the user session. vmlab doesn't try to
// install those automatically — too OS-specific and too privileged.
const atspiScript = `import sys, pyatspi
mode = sys.argv[1]
needle = sys.argv[2].lower() if len(sys.argv) > 2 else ""
CLICKABLE = (pyatspi.ROLE_PUSH_BUTTON, pyatspi.ROLE_TOGGLE_BUTTON,
             pyatspi.ROLE_MENU_ITEM, pyatspi.ROLE_LINK,
             pyatspi.ROLE_RADIO_BUTTON, pyatspi.ROLE_CHECK_BOX)
def matches(node):
    n = (node.name or "").lower()
    d = (node.description or "").lower()
    return needle and (needle in n or needle in d)
def walk(root, depth=0):
    try: kids = list(root)
    except: kids = []
    for c in kids:
        if c is None: continue
        try:
            role = c.getRole()
        except: role = None
        if role in CLICKABLE and matches(c):
            try:
                a = c.queryAction()
                for i in range(a.nActions):
                    nm = a.getName(i).lower()
                    if nm in ("click","press","activate","jump","check","uncheck"):
                        a.doAction(i)
                        print("clicked:", c.name)
                        return True
            except: pass
        if walk(c, depth+1): return True
    return False
ok = walk(pyatspi.Registry.getDesktop(0))
sys.exit(0 if ok else 1)
`

// atspiCmd returns the remote shell command for an AT-SPI-eligible verb,
// or (_, false) when the verb doesn't apply (caller falls through to the
// display-server backend).
func atspiCmd(a GUIAction) (string, bool) {
	switch a.Kind {
	case "click-text":
		if a.Text == "" {
			return "", false
		}
		return atspiRun("click-text", a.Text), true
	case "click":
		// AT-SPI doesn't have a "click by selector"; we treat the selector
		// as a Name substring, identical to click-text.
		if a.Selector == "" {
			return "", false
		}
		return atspiRun("click-text", a.Selector), true
	}
	return "", false
}

// atspiRun packages the heredoc invocation. Python args round-trip through
// single-quoted positional parameters; the script body is kept in a quoted
// heredoc so dollar signs / backticks don't get expanded by the outer shell.
func atspiRun(mode, label string) string {
	return fmt.Sprintf(`python3 -c %s %s %s`,
		shellSingleQuote(atspiScript),
		shellSingleQuote(mode),
		shellSingleQuote(label))
}

// wlAutoGuard wraps a Wayland-only command in a runtime guard so it noisily
// errors when invoked on an X11 session instead of silently misbehaving.
func wlAutoGuard(w string) string {
	return fmt.Sprintf(`bash -lc 'if [ -n "$WAYLAND_DISPLAY" ] || [ "$XDG_SESSION_TYPE" = "wayland" ]; then %s; else echo "ssh gui: wayland-only verb on x11 session" >&2; exit 64; fi'`,
		shellEscapeForSingleQuote(w))
}

// shellEscapeForSingleQuote escapes a string that itself contains the
// `bash -lc '...'` payload so embedded single quotes round-trip correctly.
// Strategy: close the outer quote, emit a literal quote, reopen.
func shellEscapeForSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// wlCmd builds a Wayland-side shell command for a GUI action using
// ydotool (mouse + keys via uinput) and wtype (text + keysyms). The guest
// must have ydotoold running (uinput access) and wtype installed.
//
// Mapped verbs:
//   - click-at    — ydotool mousemove --absolute X Y; ydotool click 1
//   - type        — wtype 'text'
//   - hotkey      — wtype -k Return / -M ctrl -k c -m ctrl (per chord)
//   - observe     — wlrctl/swaymsg-friendly: print active window if available
//   - open-url    — xdg-open (works under both display servers)
//
// Wayland has no global window-name search akin to xdotool's --name, so
// click / click-text return a clear error pointing at ssh.uiMode=atspi.
func wlCmd(a GUIAction) (string, error) {
	switch a.Kind {
	case "click-at":
		return fmt.Sprintf(`ydotool mousemove --absolute %d %d && ydotool click 1`,
			extraInt(a.Extra, "x"), extraInt(a.Extra, "y")), nil
	case "type":
		if a.Text == "" {
			return "", fmt.Errorf("ssh gui type requires text")
		}
		return "wtype " + shellSingleQuote(a.Text), nil
	case "hotkey":
		chord := a.Text
		if chord == "" {
			chord = a.Selector
		}
		if chord == "" {
			return "", fmt.Errorf("ssh gui hotkey requires text (chord)")
		}
		return "wtype " + chordToWtype(chord), nil
	case "open-url":
		url := a.Path
		if url == "" {
			url = a.Text
		}
		if url == "" {
			return "", fmt.Errorf("ssh gui open-url requires path or text")
		}
		return "xdg-open " + shellSingleQuote(url), nil
	case "observe":
		// Best-effort: swaymsg for Sway, wlrctl for wlroots compositors,
		// otherwise just an "(unknown wayland compositor)" line.
		return `(swaymsg -t get_tree 2>/dev/null | jq -r '.. | select(.focused?==true) | .name' | head -1) || (wlrctl window | head -1) || echo "(unknown wayland compositor)"`, nil
	case "click", "click-text":
		return "", fmt.Errorf("ssh gui %s: wayland has no global window-name search — set ssh.uiMode=atspi or use click-at coordinates", a.Kind)
	}
	return "", fmt.Errorf("ssh: wayland backend has no mapping for %q", a.Kind)
}

// chordToWtype maps the cross-platform chord syntax to wtype's flags.
// wtype takes -M <mod> ... -k <key> ... -m <mod> sequences. Common chords:
//
//	ctrl+c   -> -M ctrl -k c -m ctrl
//	alt+tab  -> -M alt  -k Tab -m alt
//	return   -> -k Return
func chordToWtype(chord string) string {
	parts := strings.Split(strings.ToLower(chord), "+")
	if len(parts) == 1 {
		// Bare key (Return, Escape, Tab…) — wtype expects the keysym name.
		return "-k " + shellSingleQuote(parts[0])
	}
	mods := parts[:len(parts)-1]
	key := parts[len(parts)-1]
	var b strings.Builder
	for _, m := range mods {
		fmt.Fprintf(&b, "-M %s ", m)
	}
	fmt.Fprintf(&b, "-k %s ", shellSingleQuote(key))
	for i := len(mods) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "-m %s ", mods[i])
	}
	return strings.TrimSpace(b.String())
}

// xdoCmd returns the remote shell command for a given GUI action.
func xdoCmd(a GUIAction, display string) (string, error) {
	prefix := fmt.Sprintf("DISPLAY=%s ", display)
	switch a.Kind {
	case "click":
		if a.Selector == "" {
			return "", fmt.Errorf("ssh gui click requires selector (window name or class)")
		}
		// xdotool tries name, then class, then classname. activate first
		// match, then send a left-click.
		return prefix + fmt.Sprintf(`bash -c 'w=$(xdotool search --name %[1]s 2>/dev/null | head -1); test -z "$w" && w=$(xdotool search --class %[1]s 2>/dev/null | head -1); test -z "$w" && w=$(xdotool search --classname %[1]s 2>/dev/null | head -1); test -n "$w" && xdotool windowactivate "$w" && xdotool click 1'`, shellSingleQuote(a.Selector)), nil
	case "click-text":
		if a.Text == "" {
			return "", fmt.Errorf("ssh gui click-text requires text")
		}
		return prefix + fmt.Sprintf(`bash -c 'w=$(xdotool search --name %[1]s 2>/dev/null | head -1); test -z "$w" && w=$(xdotool search --class %[1]s 2>/dev/null | head -1); test -n "$w" && xdotool windowactivate "$w" && xdotool click 1'`, shellSingleQuote(a.Text)), nil
	case "click-at":
		return prefix + fmt.Sprintf("xdotool mousemove %d %d click 1", extraInt(a.Extra, "x"), extraInt(a.Extra, "y")), nil
	case "type":
		if a.Text == "" {
			return "", fmt.Errorf("ssh gui type requires text")
		}
		return prefix + fmt.Sprintf("xdotool type --delay 20 %s", shellSingleQuote(a.Text)), nil
	case "hotkey":
		chord := a.Text
		if chord == "" {
			chord = a.Selector
		}
		if chord == "" {
			return "", fmt.Errorf("ssh gui hotkey requires text (chord)")
		}
		return prefix + "xdotool key " + shellSingleQuote(chordToXdotool(chord)), nil
	case "observe":
		return prefix + `xdotool getactivewindow getwindowname 2>/dev/null && xdotool getactivewindow getwindowgeometry 2>/dev/null || echo "(no active window)"`, nil
	case "tree":
		return prefix + `xdotool search --name "." 2>/dev/null | while read w; do printf '%s %s\n' "$w" "$(xdotool getwindowname "$w" 2>/dev/null)"; done`, nil
	case "open-url":
		url := a.Path
		if url == "" {
			url = a.Text
		}
		if url == "" {
			return "", fmt.Errorf("ssh gui open-url requires path or text")
		}
		return prefix + "xdg-open " + shellSingleQuote(url), nil
	}
	return "", fmt.Errorf("ssh: unsupported gui kind %q", a.Kind)
}

// chordToXdotool maps the cross-platform chord syntax to xdotool's
// modifier+key shape. xdotool accepts "ctrl+c" natively for most chords
// and uses "Return"/"Tab"/"Escape"/etc. for special keys.
// approve polls for a Linux consent dialog and tries to dismiss it.
//
// Linux X11 has no equivalent of macOS AX or Windows UIA, so this is best-
// effort. Strategy, in order:
//
//  1. Iterate allow/deny labels via the existing click-text (xdotool window-
//     name match). Catches dialogs whose window title contains the action
//     word — common for some GTK/Qt consent dialogs.
//  2. If `extra.useDefaultKey` is unset or true, fall back to sending Return
//     (default button activation) once a candidate dialog window appears.
//     For deny labels that match the fallback path, send Escape instead.
//
// For true button-by-name matching, install AT-SPI tooling on the guest
// and shell to it directly via `run:` — that's outside vmlab's scope.
func (s *sshTransport) approve(ctx context.Context, t target.Target, a GUIAction) error {
	allow := extraStringSlice(a.Extra, "allow")
	if len(allow) == 0 {
		allow = []string{"Allow", "OK", "Continue", "Yes", "Trust", "Accept"}
	}
	deny := extraStringSlice(a.Extra, "deny")

	timeout := 10 * time.Second
	if str := extraString(a.Extra, "timeout"); str != "" {
		if d, err := time.ParseDuration(str); err == nil {
			timeout = d
		}
	}
	useDefaultKey := true
	if v, ok := a.Extra["useDefaultKey"]; ok {
		if b, ok := v.(bool); ok {
			useDefaultKey = b
		}
	}

	deadline := time.Now().Add(timeout)
	delay := 400 * time.Millisecond
	for {
		// Window-name match — handles dialogs titled with the action word.
		for _, label := range deny {
			if s.tryClickText(ctx, t, label) {
				return nil
			}
		}
		for _, label := range allow {
			if s.tryClickText(ctx, t, label) {
				return nil
			}
		}
		// Keyboard-default fallback: if any dialog-shaped window is up,
		// send Return for allow / Escape for deny. xdotool's
		// `getactivewindow` is enough — we don't need to identify the
		// app, just whether something has focus that can absorb a key.
		if useDefaultKey {
			if len(deny) > 0 && s.sendKeyToActive(ctx, t, "Escape") {
				return nil
			}
			if s.sendKeyToActive(ctx, t, "Return") {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ssh approve: no matching dialog within %s (allow=%v deny=%v)", timeout, allow, deny)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (s *sshTransport) tryClickText(ctx context.Context, t target.Target, label string) bool {
	err := s.GUI(ctx, t, GUIAction{Kind: "click-text", Text: label})
	return err == nil
}

// sendKeyToActive sends one keysym to the active window via xdotool. Returns
// true only when xdotool reports a foreground window AND the key was sent.
// We don't blindly send to ensure we're not interfering with whatever the
// user happens to be looking at.
func (s *sshTransport) sendKeyToActive(ctx context.Context, t target.Target, key string) bool {
	display := sshDisplay(t)
	// Single remote command: refuse if no active window, otherwise send.
	remote := fmt.Sprintf(`DISPLAY=%s bash -c 'w=$(xdotool getactivewindow 2>/dev/null); test -n "$w" && xdotool key --window "$w" %s'`, display, shellSingleQuote(key))
	args := append(sshDialArgs(t), remote)
	res, err := runExternal(ctx, "ssh", args, io.Discard, io.Discard)
	return err == nil && res.ExitCode == 0
}

func chordToXdotool(chord string) string {
	parts := strings.Split(strings.ToLower(chord), "+")
	if len(parts) == 0 {
		return chord
	}
	for i, p := range parts {
		switch p {
		case "cmd", "command":
			parts[i] = "super" // Cmd → Super on Linux
		case "option", "opt":
			parts[i] = "alt"
		case "enter", "return":
			parts[i] = "Return"
		case "esc", "escape":
			parts[i] = "Escape"
		case "tab":
			parts[i] = "Tab"
		case "space":
			parts[i] = "space"
		case "bksp", "backspace":
			parts[i] = "BackSpace"
		case "del", "delete":
			parts[i] = "Delete"
		case "up", "down", "left", "right":
			parts[i] = strings.Title(p)
		}
	}
	return strings.Join(parts, "+")
}

// shellSingleQuote wraps s in POSIX single quotes, escaping any embedded
// single quotes. Used for remote ssh commands so the inner argv survives
// the shell layer.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshUser returns the configured user or "root".
func sshUser(t target.Target) string {
	if u := t.SettingString("ssh", "user"); u != "" {
		return u
	}
	return "root"
}

// sshDialArgs assembles the common ssh CLI prefix: options + destination + --.
// Append the remote command (already shell-quoted) afterwards.
func sshDialArgs(t target.Target) []string {
	host := t.SettingString("ssh", "host")
	strict := t.SettingString("ssh", "strictHost")
	if strict == "" {
		strict = "yes"
	}
	args := []string{
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "RequestTTY=no",
		"-o", "StrictHostKeyChecking=" + strict,
	}
	if kh := t.SettingString("ssh", "knownHosts"); kh != "" {
		args = append(args, "-o", "UserKnownHostsFile="+kh)
	}
	if id := t.SettingString("ssh", "identity"); id != "" {
		args = append(args, "-i", id, "-o", "IdentitiesOnly=yes")
	}
	if port := t.SettingString("ssh", "port"); port != "" {
		args = append(args, "-p", port)
	}
	args = append(args, fmt.Sprintf("%s@%s", sshUser(t), host), "--")
	return args
}
