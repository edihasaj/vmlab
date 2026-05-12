package transport

import (
	"context"
	"fmt"
	"io"
	"strings"

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
	return Caps{Shell: false, Sync: true, Install: false}
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
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return fmt.Errorf("parallels-guest: parallels.vm is required")
	}
	name := t.SettingString("parallels", "syncShareName")
	if name == "" {
		// derive from basename so the same source path stays stable.
		n := src
		for _, sep := range []string{"/", "\\"} {
			if i := strings.LastIndex(n, sep); i >= 0 {
				n = n[i+1:]
			}
		}
		if n == "" {
			n = "vmlab-sync"
		}
		name = n
	}
	args, err := prlctlArgs(t, []string{"set", vm, "--shf-host-add", name, "--path", src})
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

func (p *parallelsGuestTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("parallels-guest: empty command")
	}
	vm := t.SettingString("parallels", "vm")
	if vm == "" {
		return Result{}, fmt.Errorf("parallels-guest: parallels.vm is required")
	}
	args, err := prlctlArgs(t, append([]string{"exec", vm}, cmd...))
	if err != nil {
		return Result{}, err
	}
	return runExternal(ctx, args[0], args[1:], stdout, stderr)
}

func (p *parallelsGuestTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("parallels-guest: interactive shell not supported (use prlctl enter on the host)")
}

func (p *parallelsGuestTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	return fmt.Errorf("parallels-guest: screenshot not supported")
}

func (p *parallelsGuestTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	return fmt.Errorf("parallels-guest: gui not supported")
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
	sshArgs := []string{"ssh", "-o", "ConnectTimeout=8", "-o", "BatchMode=yes", "-o", "RequestTTY=no"}
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
