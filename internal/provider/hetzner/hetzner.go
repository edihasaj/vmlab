// Package hetzner implements the Hetzner Cloud provider. Lifecycle is driven
// by the `hcloud` CLI (no Go SDK lock-in) and the resulting box is exposed to
// the rest of vmlab via the `ssh` transport.
//
// Defaults are tuned for the "smoke test a Linux box, then destroy it" case:
// dispose.on_success = destroy, no surprise spend.
package hetzner

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

// Provider implements provider.Provider for Hetzner Cloud.
type Provider struct{}

// New returns a Hetzner provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "hetzner" }

// Doctor checks that hcloud is on PATH and a token is configured.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if _, err := exec.LookPath("hcloud"); err != nil {
		return provider.Health{OK: false, Message: "hcloud CLI not on PATH (install: brew install hcloud)"}
	}
	if i.SettingString("hetzner", "token") == "" && os.Getenv("HCLOUD_TOKEN") == "" {
		return provider.Health{OK: false, Message: "hetzner.token or HCLOUD_TOKEN required"}
	}
	out, err := p.run(ctx, i, "context", "active")
	if err != nil {
		return provider.Health{OK: false, Message: err.Error()}
	}
	return provider.Health{OK: true, Message: strings.TrimSpace(out)}
}

// Status returns the current state by describing the named server.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	name := serverName(i)
	if name == "" {
		return provider.StateUnknown, errors.New("instance name required")
	}
	out, err := p.run(ctx, i, "server", "describe", name, "-o", "json")
	if err != nil {
		if isNotFoundOutput(out) {
			return provider.StateNotFound, nil
		}
		return provider.StateUnknown, err
	}
	var sd struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &sd); err != nil {
		return provider.StateUnknown, fmt.Errorf("parse describe: %w", err)
	}
	switch strings.ToLower(sd.Status) {
	case "running":
		return provider.StateRunning, nil
	case "starting", "initializing":
		return provider.StateStarting, nil
	case "off", "stopped":
		return provider.StateStopped, nil
	}
	return provider.StateUnknown, nil
}

// Up creates the server (idempotent) and waits for SSH to answer.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	res := provider.EnsureResult{}
	prior, err := p.Status(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	res.PriorState = prior
	switch prior {
	case provider.StateRunning, provider.StateStarting:
		res.Reason = "already exists"
	case provider.StateNotFound:
		if err := p.create(ctx, i); err != nil {
			return target.Target{}, res, fmt.Errorf("hcloud server create: %w", err)
		}
		res.Changed = true
		res.Reason = "created by vmlab"
	case provider.StateStopped:
		if _, err := p.run(ctx, i, "server", "poweron", serverName(i)); err != nil {
			return target.Target{}, res, fmt.Errorf("hcloud poweron: %w", err)
		}
		res.Changed = true
		res.Reason = "powered on by vmlab"
	default:
		return target.Target{}, res, fmt.Errorf("unexpected prior state: %s", prior)
	}
	ip, err := p.publicIP(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	if err := waitForTCP(ctx, ip, 22, readyTimeout(i)); err != nil {
		return target.Target{}, res, fmt.Errorf("waitForTCP %s:22: %w", ip, err)
	}
	tgt := buildTarget(i, ip)
	return tgt, res, nil
}

// Down disposes per Dispose. Default for "no surprise spend" is destroy.
func (p *Provider) Down(ctx context.Context, i provider.Instance, d provider.Dispose) error {
	if d == provider.DisposeKeep {
		return nil
	}
	cur, err := p.Status(ctx, i)
	if err != nil {
		return err
	}
	if cur == provider.StateNotFound {
		return nil
	}
	name := serverName(i)
	switch d {
	case provider.DisposeSuspend:
		return fmt.Errorf("hetzner: suspend not supported (use poweroff or destroy)")
	case provider.DisposePowerOff:
		if cur == provider.StateStopped {
			return nil
		}
		_, err := p.run(ctx, i, "server", "poweroff", name)
		return err
	case provider.DisposeDestroy:
		_, err := p.run(ctx, i, "server", "delete", name)
		return err
	}
	return fmt.Errorf("unknown dispose: %v", d)
}

// create issues `hcloud server create` with the instance's config.
func (p *Provider) create(ctx context.Context, i provider.Instance) error {
	name := serverName(i)
	args := []string{"server", "create", "--name", name}
	args = append(args, "--type", settingOr(i, "serverType", "cax11"))
	args = append(args, "--image", settingOr(i, "image", "debian-12"))
	if loc := i.SettingString("hetzner", "location"); loc != "" {
		args = append(args, "--location", loc)
	}
	if key := i.SettingString("hetzner", "sshKey"); key != "" {
		args = append(args, "--ssh-key", key)
	}
	if ud := i.SettingString("hetzner", "userDataFile"); ud != "" {
		args = append(args, "--user-data-from-file", ud)
	}
	args = append(args, "--label", "vmlab="+name)
	_, err := p.run(ctx, i, args...)
	return err
}

// publicIP returns the server's public IPv4 address.
func (p *Provider) publicIP(ctx context.Context, i provider.Instance) (string, error) {
	out, err := p.run(ctx, i, "server", "describe", serverName(i), "-o", "json")
	if err != nil {
		return "", err
	}
	var sd struct {
		PublicNet struct {
			IPv4 struct {
				IP string `json:"ip"`
			} `json:"ipv4"`
		} `json:"public_net"`
	}
	if err := json.Unmarshal([]byte(out), &sd); err != nil {
		return "", err
	}
	if sd.PublicNet.IPv4.IP == "" {
		return "", errors.New("hetzner: server has no public IPv4")
	}
	return sd.PublicNet.IPv4.IP, nil
}

// Validate is the Provider's read-only credential dry-run. It calls
// `hcloud server-type list -o noheader` which only requires read scope
// on the token and returns non-zero on either a missing token or a
// network/permission error. Surface stderr so the operator sees the
// authentic hcloud diagnostic.
func (p *Provider) Validate(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "hcloud", "server-type", "list", "-o", "noheader")
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(buf.String())
		return fmt.Errorf("hcloud validate: %w (%s)", err, out)
	}
	return nil
}

// run shells out to hcloud, inheriting HCLOUD_TOKEN from env when present and
// from instance settings otherwise. Combines stdout+stderr.
func (p *Provider) run(ctx context.Context, i provider.Instance, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "hcloud", args...)
	cmd.Env = os.Environ()
	if tok := i.SettingString("hetzner", "token"); tok != "" {
		cmd.Env = append(cmd.Env, "HCLOUD_TOKEN="+tok)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("hcloud exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

// serverName picks the hcloud server name. Defaults to the instance name.
func serverName(i provider.Instance) string {
	if n := i.SettingString("hetzner", "name"); n != "" {
		return n
	}
	return i.Name
}

func settingOr(i provider.Instance, key, fallback string) string {
	if v := i.SettingString("hetzner", key); v != "" {
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
	return 180 * time.Second
}

// buildTarget assembles an SSH target pointing at the freshly-created server.
func buildTarget(i provider.Instance, ip string) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "ssh"
	}
	settings := map[string]any{
		"ssh": map[string]any{
			"host":       ip,
			"user":       settingOr(i, "user", "root"),
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

// WaitReady polls TCP :22 on the server's public IP until reachable.
// Implements provider.ReadyWaiter so `vmlab wait` works against Hetzner.
func (p *Provider) WaitReady(ctx context.Context, i provider.Instance) error {
	ip, err := p.publicIP(ctx, i)
	if err != nil {
		return err
	}
	return waitForTCP(ctx, ip, 22, readyTimeout(i))
}

// waitForTCP dials host:port until the deadline. No banner check — Up callers
// use the SSH transport's own command-exec for verification, not us.
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

func isNotFoundOutput(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "not found") || strings.Contains(s, "no such") || strings.Contains(s, "server not found")
}
