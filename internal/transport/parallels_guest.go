package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	//
	// Remote-host fallback: when parallels.host is set, prlctl runs on a
	// different Mac, so a laptop-local path like /Users/edi/Projects/recall
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
	mkArgs := []string{"ssh", "-o", "ConnectTimeout=8", "-o", "BatchMode=yes", "-o", "RequestTTY=no"}
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
	rsyncArgs := []string{"rsync", "-az", "--delete"}
	if port != "" {
		rsyncArgs = append(rsyncArgs, "-e", "ssh -p "+port+" -o BatchMode=yes -o ConnectTimeout=8")
	} else {
		rsyncArgs = append(rsyncArgs, "-e", "ssh -o BatchMode=yes -o ConnectTimeout=8")
	}
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
