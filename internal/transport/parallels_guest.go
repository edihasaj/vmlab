package transport

import (
	"context"
	"encoding/base64"
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

const parallelsGuestFileChunkSize = 400

// NewParallelsGuest returns the parallels-guest transport.
func NewParallelsGuest() Transport { return &parallelsGuestTransport{bin: "ssh"} }

func (p *parallelsGuestTransport) Name() string { return "parallels-guest" }

func (p *parallelsGuestTransport) Capabilities() Caps {
	return Caps{Shell: false, Sync: true, Install: false, Screenshot: true, GUI: true}
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
// ssh→prlctl→cmd.exe quoting layers AND PowerShell's own native-argument
// quoting. It ships the payload through -EncodedCommand (UTF-16LE base64) so
// nothing on the transport hops re-parses it, then launches the target via
// ProcessStartInfo with a command line we quote ourselves (cmdQuote, i.e.
// CommandLineToArgvW rules).
//
// Why not the obvious `& 'exe' 'arg1' 'arg2'` call operator: Windows PowerShell
// 5.1 (the default on every Windows guest) does NOT reliably quote arguments
// that contain spaces when it builds a *native* process's command line, so a
// payload like `sqlcmd -Q "SELECT a FROM b"` reaches sqlcmd as the separate
// tokens `SELECT`, `a`, `b`. Building the command line ourselves and handing it
// to ProcessStartInfo.Arguments (verbatim — .NET does no re-quoting) sidesteps
// that bug on every PowerShell version. UseShellExecute=$false makes the child
// inherit our stdio so output streams back through prlctl exec; `exit
// $p.ExitCode` propagates the real exit code (so flows / matrix see true
// pass/fail).
func winGuestArgv(cmd []string) ([]string, error) {
	enc := encodePowerShell(winNativeScript(cmd))
	return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", enc}, nil
}

// winNativeScript builds the PowerShell payload winGuestArgv encodes. It
// resolves cmd[0] on PATH (so bare names like `sqlcmd`/`git` work, and `.cmd`/
// `.bat` shims route through cmd.exe which CreateProcess cannot launch
// directly), then starts it with cmd[1:] quoted via cmdQuote.
func winNativeScript(cmd []string) string {
	argLine := winArgLine(cmd)
	// SilentlyContinue suppresses PowerShell's progress stream ("Preparing
	// modules for first use…"), which prlctl exec otherwise serializes as
	// CLIXML <Objs> noise onto the caller's console ahead of real output.
	return "$ErrorActionPreference='Stop'\n" +
		"$ProgressPreference='SilentlyContinue'\n" +
		"$f=" + psSingleQuote(cmd[0]) + "\n" +
		"$a=" + psSingleQuote(argLine) + "\n" +
		"$g=Get-Command -Name $f -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1\n" +
		"$exe= if ($g) { $g.Source } else { $f }\n" +
		"$si=New-Object System.Diagnostics.ProcessStartInfo\n" +
		"if ($exe -match '\\.(cmd|bat)$') { $si.FileName=$env:ComSpec; $si.Arguments='/c \"\"'+$exe+'\" '+$a+'\"' } else { $si.FileName=$exe; $si.Arguments=$a }\n" +
		"$si.UseShellExecute=$false\n" +
		"$p=[System.Diagnostics.Process]::Start($si)\n" +
		"$p.WaitForExit()\n" +
		"exit $p.ExitCode"
}

// winArgLine builds the ProcessStartInfo.Arguments string for cmd[1:].
//
// cmd.exe is special: it parses its own command tail with rules unlike the CRT
// (it does not understand the `\"`-style escaping cmdQuote emits). So when we
// launch cmd.exe with a single command payload after /c or /k — the shape
// WrapShell emits for a single command string (`cmd.exe /c <line>`) and the
// shape a `run <t> -- cmd /c "a & b"` argv collapses to — cmdQuote'ing that
// payload corrupts any quotes inside it (they arrive at cmd.exe as literal
// `\"`). Pass the payload verbatim instead and let cmd.exe do the parsing.
//
// Every other case (multiple discrete args, or a non-cmd.exe program) keeps the
// CRT-correct cmdQuote path, which is what CreateProcess/most programs expect.
func winArgLine(cmd []string) string {
	args := cmd[1:]
	if isCmdExe(cmd[0]) && len(args) == 2 && isCmdSwitch(args[0]) {
		return args[0] + " " + args[1]
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, cmdQuote(a))
	}
	return strings.Join(quoted, " ")
}

// isCmdExe reports whether name refers to the Windows command interpreter,
// tolerating a directory prefix and the optional .exe suffix (e.g. "cmd",
// "cmd.exe", `C:\Windows\System32\cmd.exe`).
func isCmdExe(name string) bool {
	base := name
	if i := strings.LastIndexAny(base, `\/`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(strings.TrimSuffix(strings.ToLower(base), ".exe"))
	return base == "cmd"
}

// isCmdSwitch reports whether arg is cmd.exe's run-command switch (/c or /k).
func isCmdSwitch(arg string) bool {
	switch strings.ToLower(arg) {
	case "/c", "/k":
		return true
	default:
		return false
	}
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
//   - click-at   — raw-coordinate left click; the reliable path for WebView2
//     (Teams/Electron/browser) inner controls that UIA can't invoke.
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
//
// Session note: `prlctl exec` runs in Session 0 (SYSTEM), which cannot inject
// input into — or read the live UIA tree of — the interactive Session 1
// desktop. Targets that set `parallels.guiSession: interactive` route input
// and readback through a one-shot scheduled task run `/ru INTERACTIVE /it`, so
// keystrokes/clicks land on the real desktop (required for Teams, browsers,
// any WebView2/Electron app).
func (p *parallelsGuestTransport) GUI(ctx context.Context, t target.Target, a GUIAction, stdout, stderr io.Writer) error {
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

	// `prlctl exec` lands in Session 0 (SYSTEM), which cannot inject input
	// into — or read the live UIA tree of — the interactive Session 1
	// desktop. So SendKeys/mouse_event no-op against real apps (Teams,
	// browsers, anything WebView2). Targets that declare
	// `parallels.guiSession: interactive` route the payload through a
	// one-shot scheduled task run `/ru INTERACTIVE /it`, which executes in
	// the logged-in user's session and actually drives the desktop.
	if strings.EqualFold(t.SettingString("parallels", "guiSession"), "interactive") {
		return p.runInteractiveGUI(ctx, t, a, script, stdout)
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
	res, err := runExternal(ctx, args[0], args[1:], stdout, &errb)
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

// Guest-side files the interactive GUI runner uses. Fixed paths under the
// world-writable Public profile so both Session 0 (bootstrap) and Session 1
// (the scheduled task) can reach them.
const (
	iguiScript = `C:\Users\Public\vmlab-gui.ps1`
	iguiTask   = `C:\Users\Public\vmlab-gui-task.cmd`
	iguiOut    = `C:\Users\Public\vmlab-gui-out.txt`
	iguiDone   = `C:\Users\Public\vmlab-gui-done.txt`
	iguiName   = "vmlabGui"
)

// runInteractiveGUI executes a winuiScript payload in the guest's interactive
// user session (Session 1) via a one-shot scheduled task, then polls for the
// completion marker and streams any captured output (observe/tree JSON) back
// to stdout. It uses only `prlctl exec` (Session 0) round-trips — no SSH or
// shared folder needed.
func (p *parallelsGuestTransport) runInteractiveGUI(ctx context.Context, t target.Target, a GUIAction, payload string, stdout io.Writer) error {
	// Wrapper (runs in Session 1): run the payload, redirect ALL its streams
	// to the out file, and always drop a done marker so the poller unblocks.
	wrapper := "$ErrorActionPreference='Stop'\r\n" +
		"try {\r\n" +
		"  & {\r\n" + payload + "\r\n  } *> '" + iguiOut + "'\r\n" +
		"  'OK' | Set-Content -LiteralPath '" + iguiDone + "' -Encoding ascii\r\n" +
		"} catch {\r\n" +
		"  \"ERR: $_\" | Out-File -FilePath '" + iguiOut + "' -Encoding utf8\r\n" +
		"  'ERR' | Set-Content -LiteralPath '" + iguiDone + "' -Encoding ascii\r\n" +
		"}\r\n"

	// The task .cmd: create + fire the interactive one-shot. Kept in a file
	// (not inline) because schtasks' /tr quoting is brittle through the exec
	// hops — a fixed-content file sidesteps it entirely.
	// -WindowStyle Hidden so the task's own powershell never becomes the
	// foreground window and steals focus from the app we're driving — keys
	// would otherwise land on the console instead of (e.g.) the Teams
	// compose box. A Task Scheduler process started hidden leaves the prior
	// foreground window focused.
	taskCmd := `schtasks /create /tn ` + iguiName +
		` /tr "powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File ` + iguiScript + `"` +
		` /sc once /st 23:59 /ru INTERACTIVE /it /f` + "\r\n" +
		`schtasks /run /tn ` + iguiName + "\r\n"

	// Ship both files in via chunked base64 writes. A single giant
	// -EncodedCommand carrying the whole payload trips prlctl exec's
	// argument-size ceiling (silent no-op), so we stay well under it — same
	// constraint `vmlab cp` documents.
	if err := p.writeGuestFile(ctx, t, iguiScript, []byte(wrapper)); err != nil {
		return fmt.Errorf("parallels-guest gui %s: write guest script: %w", a.Kind, err)
	}
	if err := p.writeGuestFile(ctx, t, iguiTask, []byte(taskCmd)); err != nil {
		return fmt.Errorf("parallels-guest gui %s: write guest task: %w", a.Kind, err)
	}

	// Bootstrap (Session 0): clear stale markers, fire the interactive task.
	bootstrap := "$ErrorActionPreference='Stop'\r\n" +
		"Remove-Item -LiteralPath '" + iguiOut + "','" + iguiDone + "' -ErrorAction SilentlyContinue\r\n" +
		"cmd /c '" + iguiTask + "'\r\n"

	if _, err := p.runGuestPS(ctx, t, bootstrap, io.Discard); err != nil {
		return fmt.Errorf("parallels-guest gui %s: launch interactive task: %w", a.Kind, err)
	}

	// Poll for the done marker. Interactive input kinds finish in <1s; give a
	// generous ceiling for slower payloads (open-url launches, tree dumps).
	deadline := 90 * time.Second
	if d := extraInt(a.Extra, "timeoutMs"); d > 0 {
		deadline = time.Duration(d) * time.Millisecond
	}
	poll := time.NewTicker(700 * time.Millisecond)
	defer poll.Stop()
	timeout := time.After(deadline)
	var status string
	for status == "" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("parallels-guest gui %s: interactive task did not complete within %s", a.Kind, deadline)
		case <-poll.C:
			var buf strings.Builder
			if _, err := p.runGuestPS(ctx, t,
				"if (Test-Path -LiteralPath '"+iguiDone+"') { Get-Content -LiteralPath '"+iguiDone+"' -Raw }", &buf); err != nil {
				return fmt.Errorf("parallels-guest gui %s: poll: %w", a.Kind, err)
			}
			status = strings.TrimSpace(buf.String())
		}
	}

	// Stream captured output (observe/tree emit JSON/text here) to stdout.
	var out strings.Builder
	if _, err := p.runGuestPS(ctx, t,
		"if (Test-Path -LiteralPath '"+iguiOut+"') { Get-Content -LiteralPath '"+iguiOut+"' -Raw }", &out); err != nil {
		return fmt.Errorf("parallels-guest gui %s: read output: %w", a.Kind, err)
	}
	if strings.HasPrefix(status, "ERR") {
		return fmt.Errorf("parallels-guest gui %s failed in guest: %s", a.Kind, strings.TrimSpace(out.String()))
	}
	if s := out.String(); s != "" {
		_, _ = io.WriteString(stdout, s)
	}
	return nil
}

// writeGuestFile reconstructs data at remotePath inside the guest by streaming
// it as base64 in small chunks (one prlctl exec per chunk), then decoding it
// there. Mirrors the cli `cp` file push but at transport level so the GUI
// runner can stage scripts without a shared folder. Chunk size stays well
// under prlctl exec's argument ceiling.
func (p *parallelsGuestTransport) writeGuestFile(ctx context.Context, t target.Target, remotePath string, data []byte) error {
	// Parallels Desktop 26.4 silently hangs around 790 raw payload characters
	// after the PowerShell command is wrapped for prlctl exec. Keep enough
	// headroom for the fixed command and path portions of the encoded payload.
	b64 := base64.StdEncoding.EncodeToString(data)
	quotedRemote := psSingleQuote(remotePath)
	quotedTmp := psSingleQuote(remotePath + ".vmlabgui")
	first := true
	for i := 0; i < len(b64); i += parallelsGuestFileChunkSize {
		end := i + parallelsGuestFileChunkSize
		if end > len(b64) {
			end = len(b64)
		}
		cmdlet := "Add-Content"
		if first {
			cmdlet = "Set-Content" // truncate any stale temp on the first chunk
		}
		script := cmdlet + " -LiteralPath " + quotedTmp + " -Value '" + b64[i:end] + "' -NoNewline"
		if _, err := p.runGuestPS(ctx, t, script, io.Discard); err != nil {
			return err
		}
		first = false
	}
	if first {
		_, err := p.runGuestPS(ctx, t, "Set-Content -LiteralPath "+quotedRemote+" -Value '' -NoNewline", io.Discard)
		return err
	}
	decode := "[IO.File]::WriteAllBytes(" + quotedRemote + ",[Convert]::FromBase64String((Get-Content -Raw -LiteralPath " + quotedTmp + "))); Remove-Item -LiteralPath " + quotedTmp
	_, err := p.runGuestPS(ctx, t, decode, io.Discard)
	return err
}

// runGuestPS runs a PowerShell script in the guest via `prlctl exec` (Session
// 0), delivered as -EncodedCommand so quoting survives the ssh→prlctl→cmd
// hops. Captures stdout into the supplied writer.
func (p *parallelsGuestTransport) runGuestPS(ctx context.Context, t target.Target, script string, stdout io.Writer) (Result, error) {
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return Result{}, fmt.Errorf("parallels-guest: parallels.vm is required")
	}
	argv := []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(script)}
	args, err := prlctlArgs(t, append([]string{"exec", vm}, argv...))
	if err != nil {
		return Result{}, err
	}
	var errb strings.Builder
	res, err := runExternal(ctx, args[0], args[1:], stdout, &errb)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("guest powershell exit=%d: %s", res.ExitCode, strings.TrimSpace(errb.String()))
	}
	return res, nil
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
	case "click-at":
		// Raw-coordinate left click. Essential for WebView2 surfaces (Teams,
		// Electron, browsers) whose inner controls don't expose reliable UIA
		// InvokePattern — falling back to a physical click at x,y is the only
		// thing that lands. Coords are absolute virtual-desktop pixels.
		x := extraInt(a.Extra, "x")
		y := extraInt(a.Extra, "y")
		return prelude + fmt.Sprintf(`Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern bool SetCursorPos(int X, int Y);[DllImport("user32.dll")]public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint cButtons, uint dwExtraInfo);' -Name U32 -Namespace W -PassThru | Out-Null
[W.U32]::SetCursorPos(%d, %d)
Start-Sleep -Milliseconds 120
[W.U32]::mouse_event(0x2, 0, 0, 0, 0)
[W.U32]::mouse_event(0x4, 0, 0, 0, 0)`, x, y), nil
	case "click-text":
		if a.Text == "" {
			return "", fmt.Errorf("parallels-guest gui click-text requires text")
		}
		// Use an exact indexed UIA lookup inside the foreground window.
		// Enumerating descendants can hang indefinitely in WebView2 providers.
		return prelude + fmt.Sprintf(`Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern System.IntPtr GetForegroundWindow();' -Name FG -Namespace W -PassThru | Out-Null
$h = [W.FG]::GetForegroundWindow()
$root = $null
if ($h -ne [System.IntPtr]::Zero) { $root = [Windows.Automation.AutomationElement]::FromHandle($h) }
if (-not $root) { $root = [Windows.Automation.AutomationElement]::RootElement }
$cond = New-Object Windows.Automation.OrCondition @(
  (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::AutomationIdProperty, %q)),
  (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty, %q))
)
$found = $root.FindFirst([Windows.Automation.TreeScope]::Descendants, $cond)
if (-not $found) { throw "no element with exact Name or AutomationId %q; use click-at for WebView content" }
$pat = $null
if ($found.TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern, [ref]$pat)) { $pat.Invoke() }
elseif ($found.TryGetCurrentPattern([Windows.Automation.TogglePattern]::Pattern, [ref]$pat)) { $pat.Toggle() }
else {
  $r = $found.Current.BoundingRectangle
  [System.Windows.Forms.Cursor]::Position = New-Object Drawing.Point ([int]($r.X + $r.Width/2)), ([int]($r.Y + $r.Height/2))
  Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint cButtons, uint dwExtraInfo);' -Name U32 -Namespace W
  [W.U32]::mouse_event(0x2, 0, 0, 0, 0); [W.U32]::mouse_event(0x4, 0, 0, 0, 0)
}`, a.Text, a.Text, a.Text), nil
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
		// Root at the foreground WINDOW (not just the focused element) so the
		// dump captures the whole app surface — the conversation/message list,
		// not only the control that happens to have keyboard focus. Falls back
		// to focused element, then the desktop root.
		return prelude + `Add-Type -MemberDefinition '[DllImport("user32.dll")]public static extern System.IntPtr GetForegroundWindow();' -Name FG -Namespace W -PassThru | Out-Null
$h = [W.FG]::GetForegroundWindow()
$root = $null
if ($h -ne [System.IntPtr]::Zero) { $root = [Windows.Automation.AutomationElement]::FromHandle($h) }
if (-not $root) { $root = [Windows.Automation.AutomationElement]::FocusedElement }
if (-not $root) { $root = [Windows.Automation.AutomationElement]::RootElement }
# Flat descendants dump: a depth-limited TreeWalker prunes before reaching
# deep WebView2/Electron content (Teams message text lives many levels down),
# so instead enumerate ALL named descendants. Capped to keep output bounded.
$all = $root.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
$n = 0
foreach ($e in $all) {
  $c = $e.Current
  if ([string]::IsNullOrWhiteSpace($c.Name)) { continue }
  "[$($c.ControlType.ProgrammaticName.Replace('ControlType.',''))] $($c.Name)" | Write-Output
  $n++
  if ($n -ge 600) { break }
}`, nil
	case "open-url":
		url := a.Path
		if url == "" {
			url = a.Text
		}
		if url == "" {
			return "", fmt.Errorf("parallels-guest gui open-url requires path or text")
		}
		return prelude + fmt.Sprintf(`Start-Process %q`, url), nil
	case "launch":
		// Start a program ON THE DESKTOP. `vmlab run` uses prlctl exec, which
		// lands in Session 0 as SYSTEM: anything it starts has no window handle
		// and is invisible to Screenshot. Routed through the interactive GUI
		// path (guiSession: interactive) this lands in the logged-in user's
		// session, so the window is real and screenshottable.
		//
		// --path is the executable and --text its arguments; when --path is
		// empty the whole of --text is parsed as one command line.
		exe, args := a.Path, a.Text
		if exe == "" {
			exe, args = splitCommandLine(a.Text)
		}
		if exe == "" {
			return "", fmt.Errorf("parallels-guest gui launch requires path (executable) or text (command line)")
		}
		// PowerShell single-quoted literals, NOT Go %q: %q escapes with
		// backslashes, which PowerShell does not treat as an escape (its escape
		// char is a backtick). A command line containing double quotes — the
		// normal case, e.g. `sqlcmd -Q "SELECT ..."` — would otherwise reach the
		// guest with stray backslashes and fail to parse.
		start := fmt.Sprintf(`$p = Start-Process -FilePath %s -PassThru`, psQuote(exe))
		if args != "" {
			start = fmt.Sprintf(`$p = Start-Process -FilePath %s -ArgumentList %s -PassThru`, psQuote(exe), psQuote(args))
		}
		// Emit the pid so callers can poll or stop it later.
		return prelude + start + "\n" + `Write-Output ("pid=" + $p.Id)`, nil
	}
	return "", fmt.Errorf("parallels-guest: unsupported gui kind %q", a.Kind)
}

// psQuote wraps s as a PowerShell single-quoted literal, doubling any embedded
// single quote. Inside '...' PowerShell performs no escape or variable
// expansion, so this is safe for arbitrary payloads — including the double
// quotes that Go's %q would mangle.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// splitCommandLine splits a single command line into the executable and the
// remaining argument string, honouring double quotes around a path that
// contains spaces (`"C:\Program Files\x\y.exe" -flag`). Only the executable
// needs unquoting — the argument remainder is handed to Start-Process
// -ArgumentList as-is, which re-parses it itself.
func splitCommandLine(cmdLine string) (exe string, args string) {
	s := strings.TrimSpace(cmdLine)
	if s == "" {
		return "", ""
	}
	if s[0] == '"' {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			return s[1 : end+1], strings.TrimSpace(s[end+2:])
		}
		return strings.Trim(s, `"`), ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
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
		// Local: rely on PATH but allow fallback to the standard app-bundle
		// location for non-login agent shells.
		bin := "prlctl"
		if alt := t.SettingString("parallels", "bin"); alt != "" {
			bin = alt
		}
		if !haveBinary(bin) {
			if cand := filepath.Join(prlPath, bin); fileReadable(cand) {
				bin = cand
			}
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

func fileReadable(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
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
