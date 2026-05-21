package transport

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"

	"github.com/edihasaj/vmlab/internal/target"
)

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
// rule is `''` for a literal single quote), encodes it, and ships it through
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
	return Caps{Shell: true, Sync: true, Install: true}
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
	res, err := runExternal(ctx, "ssh", args, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		return Health{OK: false, Message: fmt.Sprintf("ssh exit=%d", res.ExitCode)}
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
	args, err := winSSHArgs(t, cmd)
	if err != nil {
		return Result{}, err
	}
	return runExternal(ctx, "ssh", args, stdout, stderr)
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
	// Screenshots happen via a Windows-side GUI driver (see GUI). Until that
	// lands as a first-class transport it's better to surface the gap than
	// silently no-op.
	return fmt.Errorf("ssh-windows: screenshot not yet supported (M1 GUI stub pending)")
}

func (s *sshWindowsTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	return fmt.Errorf("ssh-windows: gui not yet supported (M1 GUI stub pending)")
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
