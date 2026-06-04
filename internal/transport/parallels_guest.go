package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// parallelsGuestTransport runs commands inside a Parallels guest VM via
// `prlctl exec`. When `parallels.host` is set, prlctl is executed over SSH on
// that host — the Mac that owns the VM.
//
// Quoting note: the bash smoke (scripts/smoke-parallels.sh) hit layered hell
// because each hop (ssh -> remote shell -> prlctl exec) gobbled one round of
// quoting. We solve it once here: accept []string, POSIX-quote each element
// for the remote shell, and emit a single command line. The caller does not
// need to know whether parallels.host is local or remote.
type parallelsGuestTransport struct{ bin string }

// NewParallelsGuest returns the parallels-guest transport.
func NewParallelsGuest() Transport { return &parallelsGuestTransport{bin: "ssh"} }

func (p *parallelsGuestTransport) Name() string { return "parallels-guest" }

func (p *parallelsGuestTransport) Capabilities() Caps {
	return Caps{Shell: false, Sync: true, Install: false, Screenshot: true}
}

func (p *parallelsGuestTransport) Doctor(ctx context.Context, t target.Target) Health {
	host := t.SettingString("parallels", "host")
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return Health{OK: false, Message: "parallels.vm is required"}
	}
	args, err := prlctlArgs(t, []string{"status", vm})
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if host != "" && !haveBinary("ssh") {
		return Health{OK: false, Message: "ssh not on PATH"}
	}
	res, err := runExternal(ctx, args[0], args[1:], io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		return Health{OK: false, Message: fmt.Sprintf("prlctl status exit=%d", res.ExitCode)}
	}
	return Health{OK: true, Message: "parallels VM reachable"}
}

func (p *parallelsGuestTransport) Sync(ctx context.Context, t target.Target, src string) error {
	// Live-add the source path as a Parallels shared folder. Idempotent:
	// `prlctl set --shf-host-add` failing with "already used" means the
	// folder is already mounted, which is a successful end state.
	//
	// Remote-host fallback: when parallels.host is set, prlctl runs on a
	// different Mac, so a laptop-local path like /Users/you/Projects/app
	// won't exist there. Stage the source on the remote host first via
	// rsync, then shf-host-add the staged path. Disabled by setting
	// parallels.syncStaging: false (caller knows the path is already
	// shared / accessible host-side).
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return fmt.Errorf("parallels-guest: parallels.vm is required")
	}
	name := t.SettingString("parallels", "syncShareName")
	if name == "" {
		name = shareNameFromSrc(src)
	}

	hostPath := src
	host := t.SettingString("parallels", "host")
	if host != "" && shouldStageLocally(src) && t.SettingString("parallels", "syncStaging") != "false" {
		staged, err := stageOnRemoteHost(ctx, t, host, src, name)
		if err != nil {
			return fmt.Errorf("parallels-guest sync: staging to %s failed: %w", host, err)
		}
		hostPath = staged
	}

	args, err := prlctlArgs(t, []string{"set", vm, "--shf-host-add", name, "--path", hostPath})
	if err != nil {
		return err
	}
	var buf strings.Builder
	res, runErr := runExternal(ctx, args[0], args[1:], &buf, &buf)
	if runErr == nil && res.ExitCode == 0 {
		return nil
	}
	if strings.Contains(buf.String(), "already used") {
		return nil
	}
	if runErr != nil {
		return runErr
	}
	return fmt.Errorf("parallels-guest sync: exit=%d: %s", res.ExitCode, strings.TrimSpace(buf.String()))
}

func shareNameFromSrc(src string) string {
	if src == "" {
		return "vmlab-sync"
	}
	n := src
	for _, sep := range []string{"/", "\\"} {
		if i := strings.LastIndex(n, sep); i >= 0 {
			n = n[i+1:]
		}
	}
	if n == "" || n == "." {
		// "." (or trailing-slash dir) resolves to cwd basename so sync: .
		// in a flow gets a stable name.
		if abs, err := filepath.Abs(src); err == nil {
			n = filepath.Base(abs)
		}
	}
	if n == "" {
		n = "vmlab-sync"
	}
	return n
}

// shouldStageLocally returns true when src looks like a path on the laptop
// that needs to be shipped to the remote host before shf-host-add can see it.
func shouldStageLocally(src string) bool {
	info, err := os.Stat(src)
	if err != nil {
		return false
	}
	_ = info
	return true
}

// stageOnRemoteHost rsyncs src to ~/.vmlab/cache/sync/<name> on the remote
// host and returns the resolved absolute path on the remote host. Requires
// rsync on both ends and an ssh-reachable host. Uses --delete so the staged
// copy mirrors the source content (no stale files between runs).
func stageOnRemoteHost(ctx context.Context, t target.Target, host, src, name string) (string, error) {
	if !haveBinary("rsync") {
		return "", fmt.Errorf("rsync not on PATH (needed to stage %s on %s)", src, host)
	}
	user := t.SettingString("parallels", "user")
	port := t.SettingString("parallels", "port")
	dest := host
	if user != "" {
		dest = user + "@" + host
	}
	// Stage path: ~/.vmlab/cache/sync/<name>. Resolves to /Users/<user>/...
	// on macOS hosts so prlctl can read it as the same user that owns the VM.
	remoteParent := ".vmlab/cache/sync"
	remoteAbsParent := "$HOME/" + remoteParent
	remotePath := remoteParent + "/" + name
	// Ensure parent dir exists; ssh into the host and mkdir -p.
	mkArgs := append([]string{"ssh"}, sshOpts(t)...)
	if port != "" {
		mkArgs = append(mkArgs, "-p", port)
	}
	mkArgs = append(mkArgs, dest, "mkdir -p "+remoteAbsParent+" && cd "+remoteAbsParent+" && pwd")
	var pwd strings.Builder
	res, err := runExternal(ctx, mkArgs[0], mkArgs[1:], &pwd, io.Discard)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("ssh mkdir exit=%d", res.ExitCode)
	}
	parentAbs := strings.TrimSpace(pwd.String())
	if parentAbs == "" {
		return "", fmt.Errorf("ssh mkdir: empty pwd")
	}
	stagedAbs := parentAbs + "/" + name

	// rsync src/ → host:<remotePath>/ . Trailing slash on src makes rsync
	// copy contents into the named dir rather than nesting another level.
	srcArg := strings.TrimRight(src, "/") + "/"
	rsyncArgs := []string{"rsync", "-az", "--delete", "-e", rsyncRemoteShell(t)}
	// Skip the usual suspects so we don't ship gigabytes of node_modules / build dirs.
	for _, ex := range []string{".git", "node_modules", "dist", ".next", "target", ".venv", "__pycache__"} {
		rsyncArgs = append(rsyncArgs, "--exclude", ex)
	}
	rsyncArgs = append(rsyncArgs, srcArg, dest+":"+remotePath+"/")
	var errBuf strings.Builder
	res, err = runExternal(ctx, rsyncArgs[0], rsyncArgs[1:], io.Discard, &errBuf)
	if err != nil {
		return "", fmt.Errorf("rsync: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("rsync exit=%d: %s", res.ExitCode, strings.TrimSpace(errBuf.String()))
	}
	return stagedAbs, nil
}

// GuestMount answers "where inside the guest does the synced source live?"
// Parallels mounts shared folders at \\Mac\<sharename> for Windows guests and
// /media/psf/<sharename> for Linux. Lets the flow runner expose this as
// $VMLAB_SYNC_DIR so subsequent steps don't hardcode the UNC path.
func (p *parallelsGuestTransport) GuestMount(t target.Target, src string) string {
	name := t.SettingString("parallels", "syncShareName")
	if name == "" {
		name = shareNameFromSrc(src)
	}
	switch t.OSKind() {
	case "windows":
		return `\\Mac\` + name
	case "linux":
		return "/media/psf/" + name
	}
	return ""
}

func (p *parallelsGuestTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("parallels-guest: empty command")
	}
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return Result{}, fmt.Errorf("parallels-guest: parallels.vm is required")
	}

	// Windows guests: `prlctl exec` hands the command to cmd.exe inside the
	// guest, which re-parses backslashes, pipes, carets and quotes. A bare
	// argv such as `powershell -Command "a | b"` or a path like `C:\x\y` gets
	// shredded across the ssh→prlctl→cmd.exe hops (pipes swallowed, `\d`
	// collapsed to `d`). Deliver via PowerShell -EncodedCommand (UTF-16LE
	// base64) — the same trick the GUI path already relies on — so nothing
	// downstream re-parses the payload. See winGuestArgv.
	guestCmd := cmd
	if t.OSKind() == "windows" {
		wrapped, err := winGuestArgv(cmd)
		if err != nil {
			return Result{}, err
		}
		guestCmd = wrapped
	}

	args, err := prlctlArgs(t, append([]string{"exec", vm}, guestCmd...))
	if err != nil {
		return Result{}, err
	}
	return runExternal(ctx, args[0], args[1:], stdout, stderr)
}

// winGuestArgv wraps a Windows-guest command so it survives the
// ssh→prlctl→cmd.exe quoting layers. It builds a PowerShell call pipeline
// (`& 'arg0' 'arg1' …`) that invokes the original argv verbatim, then ships it
// through -EncodedCommand so neither cmd.exe nor PowerShell re-parses anything.
// The trailing `exit $LASTEXITCODE` propagates the wrapped command's real exit
// code back to the caller (so flows / matrix see true pass/fail).
func winGuestArgv(cmd []string) ([]string, error) {
	ps, err := powershellInvocation(cmd)
	if err != nil {
		return nil, err
	}
	enc := encodePowerShell(ps + "; exit $LASTEXITCODE")
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", enc}, nil
}

func (p *parallelsGuestTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("parallels-guest: interactive shell not supported (use prlctl enter on the host)")
}

// Screenshot captures the VM display via `prlctl capture`. When parallels.host
// is set, the capture lands on the remote Mac first; we then scp it back to
// the requested local path. Idempotent: rewrites path on each call.
func (p *parallelsGuestTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return fmt.Errorf("parallels-guest: parallels.vm is required")
	}
	host := t.SettingString("parallels", "host")
	remotePath := path
	if host != "" {
		// Stage on the host's /tmp; scp pull after capture.
		remotePath = "/tmp/vmlab-capture-" + shareNameFromSrc(path) + ".png"
	}
	args, err := prlctlArgs(t, []string{"capture", vm, "--file", remotePath})
	if err != nil {
		return err
	}
	var buf strings.Builder
	res, err := runExternal(ctx, args[0], args[1:], &buf, &buf)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("prlctl capture exit=%d: %s", res.ExitCode, strings.TrimSpace(buf.String()))
	}
	if host == "" {
		return nil
	}
	// Pull back via scp.
	user := t.SettingString("parallels", "user")
	port := t.SettingString("parallels", "port")
	dest := host
	if user != "" {
		dest = user + "@" + host
	}
	scpArgs := []string{"-q"}
	if id := t.SettingString("parallels", "identity"); id != "" {
		scpArgs = append(scpArgs, "-i", id, "-o", "IdentitiesOnly=yes")
	}
	if ag := t.SettingString("parallels", "identityAgent"); ag != "" {
		scpArgs = append(scpArgs, "-o", "IdentityAgent="+ag)
	}
	if port != "" {
		scpArgs = append(scpArgs, "-P", port)
	}
	scpArgs = append(scpArgs, dest+":"+remotePath, path)
	res, err = runExternal(ctx, "scp", scpArgs, io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("scp screenshot back: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("scp screenshot back: exit=%d", res.ExitCode)
	}
	return nil
}

// GUI drives the guest desktop through PowerShell scripts that call into
// Windows' built-in UI Automation (UIAutomationClient / UIAutomationTypes)
// and SendKeys. No extra dependency on the guest — everything we need
// ships with .NET on Windows 10/11 (both x64 and ARM64).
//
// Kinds covered:
//   - screenshot — captures the desktop into a PNG inside the guest, then
//     pulls it back to the host path via prlctl-shared-folder scp fallback.
//   - click      — finds an element by AutomationId or Name and invokes it
//     via the UIA InvokePattern (falls back to mouse coords if needed).
//   - click-text — clicks the first element whose Name contains the text.
//   - type       — types into the currently focused element via SendKeys.
//   - hotkey     — sends a SendKeys chord (e.g. "^c" for Ctrl+C, "%{F4}").
//   - observe    — emits frontmost window + focused element as JSON.
//   - tree       — dumps the UIA element tree of the foreground window.
//   - wait       — host-side sleep (same model as guiport).
//   - open-url   — `Start-Process <url>`.
//
// The script is delivered via PowerShell -EncodedCommand so embedded
// quotes survive the ssh→prlctl→cmd.exe layered quoting.
func (p *parallelsGuestTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
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

	// screenshot has its own dedicated path because the PNG must come
	// back to the host. Use the existing prlctl capture path which we
	// know handles the round-trip.
	if a.Kind == "screenshot" {
		if a.Path == "" {
			return fmt.Errorf("parallels-guest gui screenshot requires path")
		}
		return p.Screenshot(ctx, t, a.Path)
	}

	script, err := winuiScript(a)
	if err != nil {
		return err
	}
	// Encode the PowerShell payload as UTF-16LE base64 (the -EncodedCommand
	// contract) to sidestep ssh→prlctl→cmd→powershell quoting.
	encoded := encodePowerShell(script)
	argv := []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded}

	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return fmt.Errorf("parallels-guest: parallels.vm is required")
	}
	args, err := prlctlArgs(t, append([]string{"exec", vm}, argv...))
	if err != nil {
		return err
	}
	var errb strings.Builder
	res, err := runExternal(ctx, args[0], args[1:], io.Discard, &errb)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errb.String())
		if msg != "" {
			return fmt.Errorf("parallels-guest gui %s exited %d: %s", a.Kind, res.ExitCode, msg)
		}
		return fmt.Errorf("parallels-guest gui %s exited %d", a.Kind, res.ExitCode)
	}
	return nil
}

// winuiScript returns the PowerShell payload that performs the requested
// GUI action on the Windows guest using built-in UI Automation APIs.
// Each kind is a self-contained script (no shared helpers needed) so the
// EncodedCommand round-trips cleanly.
func winuiScript(a GUIAction) (string, error) {
	const prelude = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
Add-Type -AssemblyName System.Windows.Forms,System.Drawing
function Find-ByText([string]$needle) {
  $root = [Windows.Automation.AutomationElement]::RootElement
  $cond = New-Object Windows.Automation.OrCondition @(
    (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::AutomationIdProperty, $needle)),
    (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty, $needle))
  )
  return $root.FindFirst([Windows.Automation.TreeScope]::Descendants, $cond)
}
`
	switch a.Kind {
	case "click":
		if a.Selector == "" {
			return "", fmt.Errorf("parallels-guest gui click requires selector (AutomationId or Name)")
		}
		return prelude + fmt.Sprintf(`$el = Find-ByText %q
if (-not $el) { throw "no element matching %q" }
$pat = $null
if ($el.TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern, [ref]$pat)) {
  $pat.Invoke()
} elseif ($el.TryGetCurrentPattern([Windows.Automation.TogglePattern]::Pattern, [ref]$pat)) {
  $pat.Toggle()
} else {
  $r = $el.Current.BoundingRectangle
  [System.Windows.Forms.Cursor]::Position = New-Object Drawing.Point ([int]($r.X + $r.Width/2)), ([int]($r.Y + $r.Height/2))
  Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint cButtons, uint dwExtraInfo);' -Name U32 -Namespace W
  [W.U32]::mouse_event(0x2, 0, 0, 0, 0); [W.U32]::mouse_event(0x4, 0, 0, 0, 0)
}`, a.Selector, a.Selector), nil
	case "click-text":
		if a.Text == "" {
			return "", fmt.Errorf("parallels-guest gui click-text requires text")
		}
		// Substring Name match — broader than Find-ByText's exact-match.
		return prelude + fmt.Sprintf(`$root = [Windows.Automation.AutomationElement]::RootElement
$walker = [Windows.Automation.TreeWalker]::ControlViewWalker
$found = $null
$queue = New-Object 'System.Collections.Queue'
$queue.Enqueue($root)
while ($queue.Count -gt 0 -and -not $found) {
  $cur = $queue.Dequeue()
  $name = $cur.Current.Name
  if ($name -and $name.Contains(%q)) { $found = $cur; break }
  $child = $walker.GetFirstChild($cur)
  while ($child) { $queue.Enqueue($child); $child = $walker.GetNextSibling($child) }
}
if (-not $found) { throw "no element with Name containing %q" }
$pat = $null
if ($found.TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern, [ref]$pat)) { $pat.Invoke() }
else { throw "element does not support InvokePattern" }`, a.Text, a.Text), nil
	case "type":
		if a.Text == "" {
			return "", fmt.Errorf("gui type requires text")
		}
		// Try SendKeys first (works in interactive session via ssh-windows);
		// fall back to UIA ValuePattern (works without session via
		// parallels-guest, when the focused element supports it).
		return prelude + fmt.Sprintf(`try {
  [System.Windows.Forms.SendKeys]::SendWait(%q)
} catch {
  $el = [Windows.Automation.AutomationElement]::FocusedElement
  if (-not $el) { throw "no focused element and SendKeys denied" }
  $pat = $null
  if ($el.TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern, [ref]$pat)) {
    $pat.SetValue(%q)
  } else {
    throw "focused element does not support ValuePattern"
  }
}`, a.Text, a.Text), nil
	case "hotkey":
		chord := a.Text
		if chord == "" {
			chord = a.Selector
		}
		if chord == "" {
			return "", fmt.Errorf("parallels-guest gui hotkey requires text (chord)")
		}
		return prelude + fmt.Sprintf(`[System.Windows.Forms.SendKeys]::SendWait(%q)`, chordToSendKeys(chord)), nil
	case "observe":
		return prelude + `$fg = [Windows.Automation.AutomationElement]::FocusedElement
if (-not $fg) { $fg = [Windows.Automation.AutomationElement]::RootElement }
$p = $fg.Current
[pscustomobject]@{name=$p.Name; class=$p.ClassName; type=$p.ControlType.ProgrammaticName; id=$p.AutomationId; rect="$($p.BoundingRectangle)"} | ConvertTo-Json`, nil
	case "tree":
		return prelude + `$root = [Windows.Automation.AutomationElement]::FocusedElement
if (-not $root) { $root = [Windows.Automation.AutomationElement]::RootElement }
function Dump-Tree($el, $depth) {
  if ($depth -gt 4) { return }
  $p = $el.Current
  ("  " * $depth) + "$($p.ControlType.ProgrammaticName) name=$($p.Name) id=$($p.AutomationId)" | Write-Output
  $walker = [Windows.Automation.TreeWalker]::ControlViewWalker
  $child = $walker.GetFirstChild($el)
  while ($child) { Dump-Tree $child ($depth + 1); $child = $walker.GetNextSibling($child) }
}
Dump-Tree $root 0`, nil
	case "open-url":
		url := a.Path
		if url == "" {
			url = a.Text
		}
		if url == "" {
			return "", fmt.Errorf("parallels-guest gui open-url requires path or text")
		}
		return prelude + fmt.Sprintf(`Start-Process %q`, url), nil
	}
	return "", fmt.Errorf("parallels-guest: unsupported gui kind %q", a.Kind)
}

// chordToSendKeys maps the cross-platform chord syntax (cmd+shift+t,
// ctrl+a, etc.) to the SendKeys notation (^a, +%{F4}). Modifiers only —
// the key portion passes through. On Windows, `cmd` is aliased to `win`
// which SendKeys can't send directly; we use `^` for Ctrl which is what
// 99% of Windows shortcuts actually use.
func chordToSendKeys(chord string) string {
	parts := strings.Split(strings.ToLower(chord), "+")
	if len(parts) == 0 {
		return chord
	}
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	var prefix string
	for _, m := range mods {
		switch m {
		case "ctrl", "control", "cmd", "command":
			prefix += "^"
		case "shift":
			prefix += "+"
		case "alt", "option", "opt":
			prefix += "%"
		}
	}
	// Map common keys to SendKeys' brace syntax.
	switch key {
	case "enter", "return":
		key = "{ENTER}"
	case "esc", "escape":
		key = "{ESC}"
	case "tab":
		key = "{TAB}"
	case "space":
		key = " "
	case "backspace", "bksp":
		key = "{BS}"
	case "delete", "del":
		key = "{DEL}"
	case "up":
		key = "{UP}"
	case "down":
		key = "{DOWN}"
	case "left":
		key = "{LEFT}"
	case "right":
		key = "{RIGHT}"
	default:
		if len(key) > 1 {
			// f1..f24, etc.
			key = "{" + strings.ToUpper(key) + "}"
		}
	}
	return prefix + key
}

// sshOpts returns the -o / -i flags this transport always uses when SSHing
// to parallels.host. Centralised so prlctlArgs / staging / screenshot scp
// all honour parallels.identity + parallels.identityAgent.
func sshOpts(t target.Target) []string {
	out := []string{"-o", "ConnectTimeout=8", "-o", "BatchMode=yes", "-o", "RequestTTY=no"}
	if id := t.SettingString("parallels", "identity"); id != "" {
		out = append(out, "-i", id, "-o", "IdentitiesOnly=yes")
	}
	if ag := t.SettingString("parallels", "identityAgent"); ag != "" {
		out = append(out, "-o", "IdentityAgent="+ag)
	}
	return out
}

// rsyncRemoteShell builds the -e value for rsync so it inherits identity /
// agent / port settings from the target.
func rsyncRemoteShell(t target.Target) string {
	parts := []string{"ssh"}
	for _, o := range sshOpts(t) {
		// shell-quote each piece since rsync hands -e to /bin/sh.
		if strings.ContainsAny(o, " '\"\\$`") {
			parts = append(parts, "'"+strings.ReplaceAll(o, "'", `'\''`)+"'")
		} else {
			parts = append(parts, o)
		}
	}
	if port := t.SettingString("parallels", "port"); port != "" {
		parts = append(parts, "-p", port)
	}
	return strings.Join(parts, " ")
}

// prlctlArgs builds the argv for invoking prlctl with the given verb+args,
// either locally or over SSH. It returns argv[0] = ssh|prlctl plus the rest.
//
// Layered quoting (the lesson from the bash smoke) is handled once: when
// host is set, every element of prlctlArgs is POSIX-quoted into a single
// remote shell command line so ssh transports it intact.
func prlctlArgs(t target.Target, prlArgs []string) ([]string, error) {
	host := t.SettingString("parallels", "host")
	prlPath := t.SettingString("parallels", "prlctlPath")
	if prlPath == "" {
		prlPath = "/Applications/Parallels Desktop.app/Contents/MacOS"
	}
	if host == "" {
		// Local: rely on PATH but allow override via prlctl.bin setting.
		bin := "prlctl"
		if alt := t.SettingString("parallels", "bin"); alt != "" {
			bin = alt
		}
		return append([]string{bin}, prlArgs...), nil
	}
	// Remote: ssh host -- "PATH=...:<path> prlctl <quoted args>"
	user := t.SettingString("parallels", "user")
	port := t.SettingString("parallels", "port")
	sshArgs := append([]string{"ssh"}, sshOpts(t)...)
	if port != "" {
		sshArgs = append(sshArgs, "-p", port)
	}
	dest := host
	if user != "" {
		dest = user + "@" + host
	}
	sshArgs = append(sshArgs, dest, "--")
	quoted := make([]string, 0, len(prlArgs))
	for _, a := range prlArgs {
		quoted = append(quoted, posixQuote(a))
	}
	remote := fmt.Sprintf("PATH=\"$PATH:%s\" prlctl %s", prlPath, strings.Join(quoted, " "))
	return append(sshArgs, remote), nil
}

// posixQuote wraps s in single quotes for a POSIX shell, escaping any embedded
// single quotes. Result is always safe to splice into a remote command line.
func posixQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`*?[]{}|&;<>()#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
