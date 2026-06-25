package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// sshMacTransport drives a remote macOS host over SSH, fanning GUI verbs
// into the remote machine's locally-installed `guiport` binary. Shell,
// sync, screenshot, and doctor reuse the plain ssh transport so the only
// new code is the GUI dispatcher and the bootstrap that confirms guiport
// is present on the far side.
//
// Setup on the remote Mac (one-time, human):
//   - install guiport (`brew install edihasaj/tap/guiport` or `make install`)
//   - grant guiport Accessibility (+ Screen Recording for pixel verbs) via
//     System Settings; for an SSH-attached session this requires the user
//     to be logged in to the GUI session at least once. Touch ID still
//     gates the toggle — same as local guiport.
//
// vmlab side: target YAML uses transport: ssh-mac with the same ssh.host /
// ssh.user / ssh.identity / etc. fields as the ssh transport. There's no
// new auth surface — it's plain OpenSSH.
type sshMacTransport struct {
	bin string        // remote binary name; defaults to "guiport"
	ssh *sshTransport // delegate for non-GUI verbs
}

// NewSSHMac returns the remote macOS GUI transport.
func NewSSHMac() Transport {
	return &sshMacTransport{
		bin: "guiport",
		ssh: &sshTransport{},
	}
}

func (s *sshMacTransport) Name() string { return "ssh-mac" }

func (s *sshMacTransport) Capabilities() Caps {
	return Caps{Shell: true, Sync: true, Install: true, Screenshot: true, GUI: true}
}

// remoteGuiport builds the remote command that runs the far Mac's locally
// installed guiport with Homebrew's bin dirs on PATH. A non-login ssh command
// shell does not source the user's profile, so a Homebrew-installed `guiport`
// (/opt/homebrew/bin on Apple Silicon, /usr/local/bin on Intel) is otherwise
// "command not found" — a false negative in Doctor and a hard failure for
// Screenshot/GUI. Skip the prefix when the binary was overridden to an
// absolute path (the caller already pinned it).
func (s *sshMacTransport) remoteGuiport(suffix string) string {
	cmd := s.bin
	if suffix != "" {
		cmd += " " + suffix
	}
	if strings.HasPrefix(s.bin, "/") {
		return cmd
	}
	return `PATH="/opt/homebrew/bin:/usr/local/bin:$PATH" ` + cmd
}

func (s *sshMacTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary("ssh") {
		return Health{OK: false, Message: "ssh not on PATH"}
	}
	if t.SettingString("ssh", "host") == "" {
		return Health{OK: false, Message: "ssh.host is required"}
	}
	// Probe: remote `guiport doctor`. Captures the same trusted/untrusted
	// breakdown the local guiport doctor surfaces, so an agent learns
	// "Accessibility not granted on remote-mac" with a single call.
	args := append(sshDialArgs(t), s.remoteGuiport("doctor"))
	var errBuf bytes.Buffer
	res, err := runExternal(ctx, "ssh", args, io.Discard, &errBuf)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		msg := firstLine(strings.TrimSpace(errBuf.String()))
		if msg == "" {
			msg = fmt.Sprintf("guiport doctor exit=%d", res.ExitCode)
		}
		return Health{OK: false, Message: "ssh-mac: " + msg}
	}
	return Health{OK: true, Message: "ssh-mac reachable; guiport ok"}
}

func (s *sshMacTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	return s.ssh.Run(ctx, t, cmd, stdout, stderr)
}

func (s *sshMacTransport) Sync(ctx context.Context, t target.Target, src string) error {
	return s.ssh.Sync(ctx, t, src)
}

func (s *sshMacTransport) Shell(ctx context.Context, t target.Target) error {
	return s.ssh.Shell(ctx, t)
}

// Screenshot captures on the remote Mac (via remote guiport) and pulls the
// resulting PNG back to the host path with scp. Two-step because guiport
// writes to a host file path locally — we can't just stream pixels back
// without a temp file on the far side.
func (s *sshMacTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	if path == "" {
		return fmt.Errorf("ssh-mac screenshot requires path")
	}
	remoteTmp := fmt.Sprintf("/tmp/vmlab-screenshot-%d.png", time.Now().UnixNano())
	// Capture on the far side.
	captureArgs := append(sshDialArgs(t), s.remoteGuiport("screenshot --out "+shellSingleQuote(remoteTmp)))
	res, err := runExternal(ctx, "ssh", captureArgs, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("ssh-mac screenshot (remote capture) exit=%d", res.ExitCode)
	}
	// Pull it back.
	defer func() {
		_, _ = runExternal(context.Background(), "ssh",
			append(sshDialArgs(t), "rm -f "+shellSingleQuote(remoteTmp)),
			io.Discard, io.Discard)
	}()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	remote := s.scpRemoteSpec(t, remoteTmp)
	scpArgs := append(sshScpArgs(t, false), remote, path)
	res, err = runExternal(ctx, "scp", scpArgs, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("ssh-mac screenshot (scp pull) exit=%d", res.ExitCode)
	}
	return nil
}

// GUI fans every verb the local guiport transport supports onto the remote
// host via SSH. The verb argv is constructed identically to guiportTransport.GUI
// so flows stay portable between mac-local-gui and ssh-mac targets.
func (s *sshMacTransport) GUI(ctx context.Context, t target.Target, a GUIAction, stdout, stderr io.Writer) error {
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
	verb, err := sshMacGuiportArgs(t, a)
	if err != nil {
		return err
	}
	args := append(sshDialArgs(t), s.remoteGuiport(verb))
	var errBuf bytes.Buffer
	res, err := runExternal(ctx, "ssh", args, stdout, &errBuf)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return fmt.Errorf("ssh-mac gui %s exited %d: %s", a.Kind, res.ExitCode, firstLine(msg))
		}
		return fmt.Errorf("ssh-mac gui %s exited %d", a.Kind, res.ExitCode)
	}
	return nil
}

// approve mirrors the guiport approve loop but routes each click-text via
// the remote binary. Same allow/deny semantics, same deny-first ordering.
func (s *sshMacTransport) approve(ctx context.Context, t target.Target, a GUIAction) error {
	allow := extraStringSlice(a.Extra, "allow")
	if len(allow) == 0 {
		allow = []string{"Allow", "OK", "Continue", "Yes", "Trust", "Open", "Always Allow", "Allow While Using App", "Allow Once"}
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
			return fmt.Errorf("ssh-mac approve: no matching dialog within %s (allow=%v deny=%v)", timeout, allow, deny)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (s *sshMacTransport) tryClickText(ctx context.Context, t target.Target, label string) bool {
	err := s.GUI(ctx, t, GUIAction{Kind: "click-text", Text: label}, io.Discard, io.Discard)
	return err == nil
}

// sshMacGuiportArgs translates a GUIAction into the guiport CLI tail that
// will be appended after `<bin> ` on the remote side. Mirrors the local
// guiport transport's verb table so behaviour matches across local/remote
// macOS targets.
func sshMacGuiportArgs(t target.Target, a GUIAction) (string, error) {
	app := ""
	if v := t.SettingString("guiport", "app"); v != "" {
		app = " --app " + shellSingleQuote(v)
	}
	switch a.Kind {
	case "click":
		if a.Selector == "" {
			return "", fmt.Errorf("ssh-mac gui click requires selector")
		}
		return "click " + shellSingleQuote(a.Selector) + app, nil
	case "click-text":
		if a.Text == "" {
			return "", fmt.Errorf("ssh-mac gui click-text requires text")
		}
		return "click-text " + shellSingleQuote(a.Text) + app, nil
	case "click-at":
		return fmt.Sprintf("click-at %d %d", extraInt(a.Extra, "x"), extraInt(a.Extra, "y")), nil
	case "type":
		if a.Text == "" {
			return "", fmt.Errorf("ssh-mac gui type requires text")
		}
		return "type " + shellSingleQuote(a.Text), nil
	case "hotkey":
		chord := a.Text
		if chord == "" {
			chord = a.Selector
		}
		if chord == "" {
			return "", fmt.Errorf("ssh-mac gui hotkey requires text (chord)")
		}
		return "hotkey " + shellSingleQuote(chord), nil
	case "observe":
		return "observe" + app, nil
	case "tree":
		return "tree" + app, nil
	case "open-url":
		url := a.Path
		if url == "" {
			url = a.Text
		}
		if url == "" {
			return "", fmt.Errorf("ssh-mac gui open-url requires path or text")
		}
		return "open-url " + shellSingleQuote(url), nil
	case "run", "run-flow":
		if a.Path == "" {
			return "", fmt.Errorf("ssh-mac gui run requires path")
		}
		return "run " + shellSingleQuote(a.Path), nil
	}
	return "", fmt.Errorf("ssh-mac: unsupported gui kind %q", a.Kind)
}

// scpRemoteSpec builds the scp source argument: [user@]host:path. Matches
// what the rsync/scp helpers in ssh.go do, but expressed at the call site
// so we don't import a transport-internal builder.
func (s *sshMacTransport) scpRemoteSpec(t target.Target, remotePath string) string {
	host := t.SettingString("ssh", "host")
	if u := sshUser(t); u != "" {
		return u + "@" + host + ":" + remotePath
	}
	return host + ":" + remotePath
}
