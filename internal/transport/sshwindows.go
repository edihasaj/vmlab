package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/edihasaj/vmlab/internal/target"
)

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// sshWindowsTransport runs commands on a remote Windows host over the OpenSSH
// server that ships with Windows 10/11 + Windows Server (the modern norm).
//
// The wire protocol is plain SSH so it works for every "Windows with OpenSSH"
// path: Parallels-Windows on a Mac, Azure/EC2 Windows, Hyper-V VMs already
// running, bare-metal Windows boxes, etc. What changes vs. transport/ssh is
// the *guest-side* shell: Windows runs cmd.exe / powershell.exe instead of a
// POSIX shell, and their quoting rules are incompatible with POSIX quoting.
//
// To dodge layered Windows quoting entirely, the default shell is pwsh with
// `-EncodedCommand` (UTF-16LE base64). The agent assembles one PowerShell
// pipeline like `& 'arg0' 'arg1' …` with single-quote escaping (PowerShell's
// rule is `”` for a literal single quote), encodes it, and ships it through
// ssh as a single inert token. No shell on either side gets a chance to
// re-interpret special characters.
//
// Settings (all under ssh.*):
//
//	ssh.host        required
//	ssh.user        defaults to "Administrator"
//	ssh.port        defaults to "22"
//	ssh.identity    path to private key
//	ssh.knownHosts  pin host keys
//	ssh.strictHost  default "yes" — flip to "accept-new" for first-contact
//	ssh.shell       "pwsh" (default) | "powershell" | "cmd" | "none"
//	                 - pwsh/powershell: -EncodedCommand path (recommended)
//	                 - cmd: cmd.exe /c <best-effort-quoted>
//	                 - none: joins args with spaces verbatim (caller owns
//	                   quoting; useful when the remote is wrapped by a
//	                   ForceCommand that already does its own parsing)
type sshWindowsTransport struct{}

// NewSSHWindows returns the ssh-windows transport.
func NewSSHWindows() Transport { return &sshWindowsTransport{} }

func (s *sshWindowsTransport) Name() string { return "ssh-windows" }

func (s *sshWindowsTransport) Capabilities() Caps {
	return Caps{Shell: true, Sync: true, Install: true, Screenshot: true}
}

func (s *sshWindowsTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary("ssh") {
		return Health{OK: false, Message: "ssh not on PATH"}
	}
	if t.SettingString("ssh", "host") == "" {
		return Health{OK: false, Message: "ssh.host is required"}
	}
	// Probe with a shell-appropriate no-op so we exercise the chosen path.
	probe := []string{"$PSVersionTable.PSVersion.ToString()"}
	if winShell(t) == "cmd" {
		probe = []string{"ver"}
	}
	args, err := winSSHArgs(t, probe)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	var errBuf bytes.Buffer
	res, err := runExternal(ctx, "ssh", args, io.Discard, &errBuf)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = fmt.Sprintf("ssh exit=%d", res.ExitCode)
		} else {
			msg = fmt.Sprintf("ssh exit=%d: %s", res.ExitCode, firstLine(msg))
		}
		return Health{OK: false, Message: msg}
	}
	return Health{OK: true, Message: "ssh-windows reachable"}
}

func (s *sshWindowsTransport) Sync(ctx context.Context, t target.Target, src string) error {
	host := t.SettingString("ssh", "host")
	user := winSSHUser(t)
	dest := t.SettingString("ssh", "dest")
	if dest == "" {
		// Windows OpenSSH defaults dest of "" to the user's home directory
		// (C:\Users\<user>), which is the sensible parallel to ~ on POSIX.
		dest = "."
	}
	if haveBinary("rsync") {
		rsh := "ssh -o BatchMode=yes -o StrictHostKeyChecking=" + winSSHStrict(t)
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
		return fmt.Errorf("ssh-windows: neither rsync nor scp on PATH")
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

func (s *sshWindowsTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("ssh-windows: empty command")
	}
	if t.Setting("ssh", "elevated") == true {
		return s.runElevated(ctx, t, cmd, stdout, stderr)
	}
	args, err := winSSHArgs(t, cmd)
	if err != nil {
		return Result{}, err
	}
	return runExternal(ctx, "ssh", args, stdout, stderr)
}

// runElevated routes the command through the vmlab-elevated scheduled task
// (registered once via `vmlab elevate setup`). Pattern:
//
//  1. Stage the command into C:\ProgramData\vmlab\inbox\next.ps1 (the
//     fixed action target of the scheduled task) and a per-call outbox
//     path for stdout/stderr capture.
//  2. Trigger via `schtasks /run /tn <task>` — runs as SYSTEM, no UAC.
//  3. Poll the outbox for the result marker; surface stdout / stderr /
//     exit-code in the Result.
//
// Concurrency: a per-target file lock prevents two callers from racing the
// shared inbox file. Acquired on the host side (we hold it for the whole
// remote round-trip) rather than the guest side, which avoids a second
// remote round-trip just to coordinate.
func (s *sshWindowsTransport) runElevated(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	taskName := t.SettingString("ssh", "elevatedTask")
	if taskName == "" {
		taskName = "vmlab-elevated"
	}
	// Compose the PowerShell payload that the SYSTEM task will execute.
	// We capture stdout / stderr / exit-code into the outbox so the host
	// can fetch them deterministically. UTF-8 throughout — Windows PowerShell
	// 5.1 defaults to UTF-16LE; we explicitly set Encoding UTF8 to keep the
	// round-trip lossless.
	payload := winElevatedPayload(cmd)
	inboxScript := payload.scriptBody + "\nGet-Content -Path " + payload.outboxJSON
	// First: drop inbox + stdout/stderr/exit-code marker. Each call uses
	// the same fixed inbox.ps1 (the task's action argument) plus a unique
	// outbox suffix that the host polls for.
	stageScript := fmt.Sprintf(`$ErrorActionPreference='Stop'
$inbox  = 'C:\ProgramData\vmlab\inbox\next.ps1'
$outbox = %s
Remove-Item $outbox -ErrorAction SilentlyContinue
Set-Content -Path $inbox -Value @'
%s
'@ -Encoding UTF8
schtasks /run /tn %s | Out-Null
$deadline = (Get-Date).AddSeconds(%d)
while (-not (Test-Path $outbox) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 200 }
if (-not (Test-Path $outbox)) { Write-Error 'vmlab-elevated: task did not write outbox in time'; exit 124 }
Get-Content -Raw $outbox
`, posixSingleQuote(payload.outboxPath), inboxScript, posixSingleQuote(taskName), int(elevatedRunTimeout(t).Seconds()))
	args, err := winSSHArgs(t, []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", stageScript})
	if err != nil {
		return Result{}, err
	}
	var raw bytes.Buffer
	res, err := runExternal(ctx, "ssh", args, &raw, stderr)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("ssh-windows elevated: stage/run exit=%d", res.ExitCode)
	}
	// raw is the outbox JSON the SYSTEM task wrote. Parse out the wrapped
	// stdout / stderr / exit-code so the caller's Result reflects the
	// elevated execution (not the staging shim).
	return parseElevatedOutbox(raw.Bytes(), stdout, stderr)
}

// elevatedRunTimeout returns the per-call timeout the staging shim waits
// for the SYSTEM task to write its outbox. Default 60s; override via
// ssh.elevatedTimeout (Go duration string).
func elevatedRunTimeout(t target.Target) time.Duration {
	if s := t.SettingString("ssh", "elevatedTimeout"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return 60 * time.Second
}

// posixSingleQuote wraps a string in PowerShell single quotes, doubling
// embedded single quotes to escape them (PowerShell's standard quoting).
func posixSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (s *sshWindowsTransport) Shell(ctx context.Context, t target.Target) error {
	host := t.SettingString("ssh", "host")
	if host == "" {
		return fmt.Errorf("ssh-windows: ssh.host is required")
	}
	args := winSSHDialArgs(t, true)
	// drop the trailing "--" — we want an interactive login shell, not a
	// remote command. The remote sshd will pick its default shell (usually
	// cmd.exe on Windows; configure DefaultShell in the registry to switch).
	if n := len(args); n > 0 && args[n-1] == "--" {
		args = args[:n-1]
	}
	return shellInteractive(ctx, "ssh", args)
}

func (s *sshWindowsTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	// Try a PowerShell-based screen grab: the System.Windows.Forms +
	// System.Drawing route works without any extra packages on stock Windows
	// 10/11 desktops with a primary display. Headless Server Core / SSH-only
	// hosts (no graphics stack loaded) will fail here — that's expected; for
	// those, drive a UI tool over RDP/VNC instead.
	if path == "" {
		return fmt.Errorf("ssh-windows: screenshot needs a destination path")
	}
	remoteTmp := `$env:TEMP + '\vmlab-shot.png'`
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms;",
		"Add-Type -AssemblyName System.Drawing;",
		"$b = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds;",
		"$bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height;",
		"$g = [System.Drawing.Graphics]::FromImage($bmp);",
		"$g.CopyFromScreen($b.Location, [System.Drawing.Point]::Empty, $b.Size);",
		"$out = " + remoteTmp + ";",
		"$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png);",
		"[Console]::Out.Write($out)",
	}, " ")
	// Force PowerShell for the capture regardless of the configured shell —
	// cmd.exe cannot drive .NET assemblies directly. Build the remote command
	// directly with -EncodedCommand so we don't touch the target's settings.
	dial := winSSHDialArgs(t, false)
	enc := encodePowerShell("& { " + script + " }")
	remoteCmd := "powershell -NoProfile -NonInteractive -EncodedCommand " + enc
	sshArgs := append(dial, remoteCmd)
	var outBuf strings.Builder
	res, err := runExternal(ctx, "ssh", sshArgs, &outBuf, io.Discard)
	if err != nil {
		return fmt.Errorf("ssh-windows screenshot: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("ssh-windows screenshot exit=%d", res.ExitCode)
	}
	remotePath := strings.TrimSpace(outBuf.String())
	if remotePath == "" {
		return fmt.Errorf("ssh-windows: remote screenshot returned empty path")
	}
	// Pull the PNG back via scp. rsync would also work but scp is universal.
	host := t.SettingString("ssh", "host")
	user := winSSHUser(t)
	args := []string{"-q", "-o", "StrictHostKeyChecking=" + winSSHStrict(t)}
	if id := t.SettingString("ssh", "identity"); id != "" {
		args = append(args, "-i", id)
	}
	if port := t.SettingString("ssh", "port"); port != "" {
		args = append(args, "-P", port)
	}
	args = append(args, fmt.Sprintf("%s@%s:%s", user, host, remotePath), path)
	res, err = runExternal(ctx, "scp", args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("scp screenshot exit=%d", res.ExitCode)
	}
	return nil
}

// GUI drives Windows UI through PowerShell + UIA / SendKeys over SSH.
// Unlike parallels-guest's GUI() (which runs out-of-session and trips UIPI),
// OpenSSH on Windows attaches to the logged-in user's interactive desktop
// session, so SendKeys + ValuePattern actually work. Verbs map to UIA
// patterns where possible and fall back to SendKeys for input.
//
// Kinds covered:
//   - screenshot — captures the desktop via System.Drawing.Bitmap; same path
//     as Screenshot() above, just routed through GUIAction.Path.
//   - click      — UIA InvokePattern on element matching AutomationId/Name
//   - click-text — UIA tree walk by Name substring, then Invoke
//   - type       — System.Windows.Forms.SendKeys (works in-session)
//   - hotkey     — SendKeys chord (cross-platform syntax translated)
//   - wait       — transport-side sleep
//   - observe    — focused window/element metadata as JSON
//   - tree       — top-N child UIA elements of the foreground window
//   - open-url   — Start-Process <url>
func (s *sshWindowsTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
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
		return s.Screenshot(ctx, t, a.Path)
	}
	if a.Kind == "approve" {
		return s.approve(ctx, t, a)
	}
	script, err := winuiScript(a)
	if err != nil {
		return err
	}
	enc := encodePowerShell("& { " + script + " }")
	dial := winSSHDialArgs(t, false)
	remoteCmd := "powershell -NoProfile -NonInteractive -EncodedCommand " + enc
	sshArgs := append(dial, remoteCmd)
	var errb strings.Builder
	res, err := runExternal(ctx, "ssh", sshArgs, io.Discard, &errb)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errb.String())
		if msg != "" {
			return fmt.Errorf("ssh-windows gui %s exited %d: %s", a.Kind, res.ExitCode, msg)
		}
		return fmt.Errorf("ssh-windows gui %s exited %d", a.Kind, res.ExitCode)
	}
	return nil
}

// approve polls for a consent dialog and clicks the first matching button via
// UIA Name-substring match. Same shape as guiport's approve — deny labels
// checked before allow so an explicit refuse beats a generic Allow. The
// per-attempt invocation is the existing click-text path, so this benefits
// from all the UIA wiring (window walk, InvokePattern, error reporting).
//
// Limitations: UAC consent (secure desktop) is unreachable here by design.
// For elevation, pre-bootstrap a SYSTEM scheduled task on the target so the
// elevated process never has to prompt.
func (s *sshWindowsTransport) approve(ctx context.Context, t target.Target, a GUIAction) error {
	allow := extraStringSlice(a.Extra, "allow")
	if len(allow) == 0 {
		allow = []string{"Allow access", "Allow", "Yes", "OK", "Continue", "Accept", "Trust"}
	}
	deny := extraStringSlice(a.Extra, "deny")

	timeout := 10 * time.Second
	if str := extraString(a.Extra, "timeout"); str != "" {
		if d, err := time.ParseDuration(str); err == nil {
			timeout = d
		}
	}
	deadline := time.Now().Add(timeout)
	delay := 400 * time.Millisecond
	for {
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
		if time.Now().After(deadline) {
			return fmt.Errorf("ssh-windows approve: no matching dialog within %s (allow=%v deny=%v)", timeout, allow, deny)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// tryClickText invokes the existing click-text GUI path silently. UIA click-
// text returns non-zero when no button matches, which is the polling signal.
func (s *sshWindowsTransport) tryClickText(ctx context.Context, t target.Target, label string) bool {
	err := s.GUI(ctx, t, GUIAction{Kind: "click-text", Text: label})
	return err == nil
}

// winShell returns the configured Windows-side shell choice.
func winShell(t target.Target) string {
	s := strings.ToLower(strings.TrimSpace(t.SettingString("ssh", "shell")))
	switch s {
	case "pwsh", "powershell", "cmd", "none":
		return s
	}
	return "pwsh"
}

func winSSHUser(t target.Target) string {
	if u := t.SettingString("ssh", "user"); u != "" {
		return u
	}
	return "Administrator"
}

func winSSHStrict(t target.Target) string {
	if s := t.SettingString("ssh", "strictHost"); s != "" {
		return s
	}
	return "yes"
}

// winSSHDialArgs builds the common ssh CLI prefix. `interactive` flips
// RequestTTY to "yes" for `vmlab shell`.
func winSSHDialArgs(t target.Target, interactive bool) []string {
	host := t.SettingString("ssh", "host")
	tty := "no"
	if interactive {
		tty = "yes"
	}
	args := []string{
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "RequestTTY=" + tty,
		"-o", "StrictHostKeyChecking=" + winSSHStrict(t),
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
	args = append(args, fmt.Sprintf("%s@%s", winSSHUser(t), host), "--")
	return args
}

// winSSHArgs returns argv for `ssh … <remote-command>` where the remote
// command is shaped per the chosen Windows shell.
func winSSHArgs(t target.Target, cmd []string) ([]string, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("ssh-windows: empty command")
	}
	args := winSSHDialArgs(t, false)
	remote, err := winRemoteCommand(t, cmd)
	if err != nil {
		return nil, err
	}
	return append(args, remote), nil
}

// winRemoteCommand renders cmd into a single string the remote sshd will hand
// to its default shell (cmd.exe on Windows, unless the user changed it). The
// shape depends on the configured ssh.shell.
func winRemoteCommand(t target.Target, cmd []string) (string, error) {
	switch winShell(t) {
	case "pwsh", "powershell":
		// Build a PowerShell pipeline `& 'arg0' 'arg1' …`, then ship it via
		// -EncodedCommand so neither cmd.exe (the default sshd shell) nor
		// PowerShell itself re-parses anything.
		bin := "pwsh"
		if winShell(t) == "powershell" {
			bin = "powershell"
		}
		ps, err := powershellInvocation(cmd)
		if err != nil {
			return "", err
		}
		enc := encodePowerShell(ps)
		return fmt.Sprintf("%s -NoProfile -NonInteractive -EncodedCommand %s", bin, enc), nil
	case "cmd":
		// Best-effort cmd.exe quoting: wrap each arg in double quotes and
		// double-up embedded double quotes. The remote sshd already hands us
		// to cmd.exe, so we just supply the joined args.
		quoted := make([]string, 0, len(cmd))
		for _, a := range cmd {
			quoted = append(quoted, cmdQuote(a))
		}
		return strings.Join(quoted, " "), nil
	case "none":
		return strings.Join(cmd, " "), nil
	}
	return "", fmt.Errorf("ssh-windows: unknown shell")
}

// powershellInvocation returns a PowerShell command line that invokes cmd[0]
// with cmd[1:] as literal positional arguments. Uses `&` (the call operator)
// so cmd[0] may include a path with spaces.
func powershellInvocation(cmd []string) (string, error) {
	parts := make([]string, 0, len(cmd))
	for i, a := range cmd {
		parts = append(parts, psSingleQuote(a))
		_ = i
	}
	// `& 'foo.exe' 'arg1' 'arg2'` is the canonical pattern. The first &
	// turns the leading string into a callable.
	return "& " + strings.Join(parts, " "), nil
}

// psSingleQuote wraps s in PowerShell single quotes. Inside single-quoted
// strings PowerShell only treats `'` specially, escaped by doubling.
func psSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// encodePowerShell encodes a PowerShell command string as UTF-16LE base64,
// the format `-EncodedCommand` requires.
func encodePowerShell(ps string) string {
	utf := utf16.Encode([]rune(ps))
	buf := make([]byte, 0, len(utf)*2)
	for _, r := range utf {
		buf = append(buf, byte(r), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// cmdQuote does best-effort cmd.exe argument quoting: wrap in double quotes
// and escape embedded `"` as `\"`. Trailing backslashes inside a quoted
// argument need doubling per the standard CRT parsing rules.
func cmdQuote(s string) string {
	if s == "" {
		return `""`
	}
	// cmd.exe only treats `\` as special when it sits next to `"` inside a
	// quoted token. A bare backslash (e.g. C:\Foo\bar) is verbatim and does
	// not require quoting on its own.
	if !strings.ContainsAny(s, ` "&<>|^`) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			backslashes++
		case '"':
			// each pending backslash doubles, plus one for the escaped quote
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

// elevatedPayload bundles the inbox script body, the per-call outbox path
// (where the SYSTEM task writes captured stdout/stderr/exit), and a stable
// outboxJSON expression used inside the inbox script to point at the same
// file. Two values so the inbox script can embed the path verbatim while
// the host-side stage script uses the PowerShell-quoted form.
type elevatedPayload struct {
	scriptBody string // the body the SYSTEM task runs
	outboxPath string // raw path; the host stage script quotes this
	outboxJSON string // already-quoted path for embedding in PowerShell
}

// winElevatedPayload composes the script the scheduled task will run. It
// invokes the caller's command, captures stdout/stderr/exit-code, and
// writes them as a small machine-readable record to outboxPath. The host
// polls for that file and parses it via parseElevatedOutbox.
//
// Command shape: the caller's argv (e.g. ["powershell.exe","-Command","ls"])
// is invoked as-is via the call operator (`& <exe> <args...>`). We do not
// re-quote each arg — they're already in the form winSSHArgs would have
// shipped, and the round-trip is local to the task.
func winElevatedPayload(cmd []string) elevatedPayload {
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), len(cmd))
	outbox := "C:\\ProgramData\\vmlab\\outbox\\" + id + ".out"
	// Build PowerShell argv literally: ($args -as [string[]]) keeps the call
	// operator happy when the array has more than one element.
	var argList strings.Builder
	for i, a := range cmd {
		if i > 0 {
			argList.WriteString(",")
		}
		argList.WriteString(posixSingleQuote(a))
	}
	body := fmt.Sprintf(`$argv = @(%s)
$outbox = %s
$tmpOut = [System.IO.Path]::GetTempFileName()
$tmpErr = [System.IO.Path]::GetTempFileName()
$proc = Start-Process -FilePath $argv[0] -ArgumentList ($argv | Select-Object -Skip 1) -NoNewWindow -PassThru -Wait -RedirectStandardOutput $tmpOut -RedirectStandardError $tmpErr
$ec = $proc.ExitCode
$out = Get-Content -Raw -Path $tmpOut -ErrorAction SilentlyContinue
$err = Get-Content -Raw -Path $tmpErr -ErrorAction SilentlyContinue
Remove-Item $tmpOut, $tmpErr -ErrorAction SilentlyContinue
$payload = @{ exitCode = $ec; stdout = $out; stderr = $err } | ConvertTo-Json -Compress
Set-Content -Path $outbox -Value $payload -Encoding UTF8`, argList.String(), posixSingleQuote(outbox))
	return elevatedPayload{scriptBody: body, outboxPath: outbox, outboxJSON: posixSingleQuote(outbox)}
}

// parseElevatedOutbox reads the JSON the SYSTEM task wrote and projects
// stdout / stderr / exit-code onto the caller's writers + Result.
func parseElevatedOutbox(raw []byte, stdout, stderr io.Writer) (Result, error) {
	// The outbox is a single-line JSON object. Trim a UTF-8 BOM defensively
	// — PowerShell 5.1 still occasionally emits one.
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\xef\xbb\xbf"))
	if text == "" {
		return Result{}, fmt.Errorf("ssh-windows elevated: empty outbox")
	}
	var rec struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(text), &rec); err != nil {
		return Result{}, fmt.Errorf("ssh-windows elevated: parse outbox: %w (raw=%q)", err, text)
	}
	if stdout != nil && rec.Stdout != "" {
		_, _ = io.WriteString(stdout, rec.Stdout)
	}
	if stderr != nil && rec.Stderr != "" {
		_, _ = io.WriteString(stderr, rec.Stderr)
	}
	return Result{ExitCode: rec.ExitCode}, nil
}
