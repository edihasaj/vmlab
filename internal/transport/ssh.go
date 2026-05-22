package transport

import (
	"context"
	"fmt"
	"io"
	"strings"

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
	res, err := runExternal(ctx, "ssh", args, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		return Health{OK: false, Message: fmt.Sprintf("ssh exit=%d", res.ExitCode)}
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

func (s *sshTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	return fmt.Errorf("ssh: screenshot not supported")
}

func (s *sshTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	return fmt.Errorf("ssh: gui not supported")
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
