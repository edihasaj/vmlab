// Package tart implements the Tart provider for Apple Silicon macOS/Linux
// guests. Tart (https://github.com/cirruslabs/tart) is the natural fit for
// "give me a clean macOS 14 in 10 seconds on my M-series Mac" without paying
// EC2 Mac rates.
//
// Lifecycle uses the `tart` CLI; output is exposed via the ssh transport.
// Up clones from tart.source if the VM doesn't yet exist, then backgrounds
// `tart run --no-graphics` and polls `tart ip --wait`. Down suspends by
// default (fast restart); destroy calls `tart delete`.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
)

// Provider implements provider.Provider for Tart.
type Provider struct{}

// New returns a Tart provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "tart" }

// Doctor checks the tart CLI is installed.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if _, err := exec.LookPath("tart"); err != nil {
		return provider.Health{OK: false, Message: "tart CLI not on PATH (install: brew install cirruslabs/cli/tart)"}
	}
	if _, err := p.run(ctx, "list", "--format=json"); err != nil {
		return provider.Health{OK: false, Message: "tart list: " + err.Error()}
	}
	return provider.Health{OK: true, Message: "tart ready"}
}

// listEntry mirrors the relevant subset of `tart list --format=json` output.
type listEntry struct {
	Name    string `json:"Name"`
	State   string `json:"State"`
	Running bool   `json:"Running"`
	Source  string `json:"Source"`
}

// findVM returns the matching list entry (zero-valued if not present).
func (p *Provider) findVM(ctx context.Context, name string) (listEntry, bool, error) {
	out, err := p.run(ctx, "list", "--format=json")
	if err != nil {
		return listEntry{}, false, err
	}
	var entries []listEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &entries); err != nil {
		return listEntry{}, false, fmt.Errorf("parse tart list: %w", err)
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true, nil
		}
	}
	return listEntry{}, false, nil
}

// Status describes the named VM.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	e, ok, err := p.findVM(ctx, vmName(i))
	if err != nil {
		return provider.StateUnknown, err
	}
	if !ok {
		return provider.StateNotFound, nil
	}
	if e.Running {
		return provider.StateRunning, nil
	}
	switch strings.ToLower(e.State) {
	case "running":
		return provider.StateRunning, nil
	case "suspended":
		return provider.StateStopped, nil
	case "stopped":
		return provider.StateStopped, nil
	}
	return provider.StateUnknown, nil
}

// Up clones from tart.source when missing, then backgrounds `tart run` and
// polls `tart ip --wait` for the guest IP.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	res := provider.EnsureResult{}
	prior, err := p.Status(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	res.PriorState = prior
	name := vmName(i)
	switch prior {
	case provider.StateRunning, provider.StateStarting:
		res.Reason = "already running"
	case provider.StateNotFound:
		src := i.SettingString("tart", "source")
		if src == "" {
			return target.Target{}, res, errors.New("tart.source is required to clone a fresh VM")
		}
		if _, err := p.run(ctx, "clone", src, name); err != nil {
			return target.Target{}, res, fmt.Errorf("tart clone %s -> %s: %w", src, name, err)
		}
		if err := p.backgroundRun(ctx, name); err != nil {
			return target.Target{}, res, err
		}
		res.Changed = true
		res.Reason = "cloned and started by vmlab"
	case provider.StateStopped:
		if err := p.backgroundRun(ctx, name); err != nil {
			return target.Target{}, res, err
		}
		res.Changed = true
		res.Reason = "started by vmlab"
	default:
		return target.Target{}, res, fmt.Errorf("unexpected prior state: %s", prior)
	}
	ip, err := p.ipWait(ctx, i, readyTimeout(i))
	if err != nil {
		return target.Target{}, res, err
	}
	return buildTarget(i, ip), res, nil
}

// Down: suspend → `tart suspend`; poweroff/destroy → `tart stop` + (destroy
// only) `tart delete`.
func (p *Provider) Down(ctx context.Context, i provider.Instance, d provider.Dispose) error {
	if d == provider.DisposeKeep {
		return nil
	}
	e, ok, err := p.findVM(ctx, vmName(i))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	name := vmName(i)
	switch d {
	case provider.DisposeSuspend:
		if !e.Running {
			return nil
		}
		_, err := p.run(ctx, "suspend", name)
		return err
	case provider.DisposePowerOff:
		if !e.Running {
			return nil
		}
		_, err := p.run(ctx, "stop", name)
		return err
	case provider.DisposeDestroy:
		if e.Running {
			if _, err := p.run(ctx, "stop", name); err != nil {
				return err
			}
		}
		_, err := p.run(ctx, "delete", name)
		return err
	}
	return fmt.Errorf("unknown dispose: %v", d)
}

// backgroundRun launches `tart run <name> --no-graphics` detached so vmlab's
// process can return after Up succeeds. Output goes to a log file under
// ~/.vmlab/tart/<name>.log for post-hoc inspection.
func (p *Provider) backgroundRun(ctx context.Context, name string) error {
	home, _ := os.UserHomeDir()
	logDir := home + "/.vmlab/tart"
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("tart: log dir: %w", err)
	}
	logPath := fmt.Sprintf("%s/%s.log", logDir, name)
	// Use sh to fully detach. `&` puts the job in background; `disown` (zsh)
	// or `nohup` keeps it alive after our process exits.
	shCmd := fmt.Sprintf("nohup tart run %q --no-graphics >> %q 2>&1 & echo $!",
		name, logPath)
	cmd := exec.CommandContext(ctx, "sh", "-c", shCmd)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tart run (background): %w: %s", err, buf.String())
	}
	return nil
}

// ipWait polls `tart ip --wait <secs> <name>` until an IP is returned or the
// timeout elapses.
func (p *Provider) ipWait(ctx context.Context, i provider.Instance, timeout time.Duration) (string, error) {
	secs := int(timeout.Seconds())
	if secs < 5 {
		secs = 30
	}
	out, err := p.run(ctx, "ip", "--wait", fmt.Sprintf("%d", secs), vmName(i))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", errors.New("tart ip returned empty")
	}
	return ip, nil
}

// run shells out to tart and combines stdout+stderr for diagnostic parsing.
func (p *Provider) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tart", args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("tart exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

func vmName(i provider.Instance) string {
	if n := i.SettingString("tart", "name"); n != "" {
		return n
	}
	return i.Name
}

func settingOr(i provider.Instance, key, fallback string) string {
	if v := i.SettingString("tart", key); v != "" {
		return v
	}
	return fallback
}

func readyTimeout(i provider.Instance) time.Duration {
	if i.Ready.Timeout != "" {
		if d, err := time.ParseDuration(i.Ready.Timeout); err == nil {
			return d
		}
	}
	return 120 * time.Second
}

func buildTarget(i provider.Instance, ip string) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "ssh"
	}
	settings := map[string]any{
		"ssh": map[string]any{
			"host":       ip,
			"user":       settingOr(i, "user", "admin"),
			"strictHost": "accept-new",
		},
	}
	if id := i.SettingString("ssh", "identity"); id != "" {
		settings["ssh"].(map[string]any)["identity"] = id
	}
	return target.Target{
		Name:      i.Name,
		Transport: tt,
		Tags:      i.Tags,
		Settings:  settings,
	}
}

// WaitReady polls `tart ip` until an address comes back.
// Implements provider.ReadyWaiter so `vmlab wait` works against Tart.
func (p *Provider) WaitReady(ctx context.Context, i provider.Instance) error {
	ip, err := p.ipWait(ctx, i, readyTimeout(i))
	if err != nil {
		return err
	}
	return waitForTCP(ctx, ip, 22, readyTimeout(i))
}

func waitForTCP(ctx context.Context, host string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(timeout)
	delay := time.Second
	for {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		c, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
		cancel()
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not reachable after %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 4*time.Second {
			delay += time.Second
		}
	}
}
