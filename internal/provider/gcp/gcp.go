// Package gcp implements the Google Cloud Platform provider. Lifecycle is
// driven by the `gcloud compute` CLI; output is exposed via the ssh transport.
//
// Defaults are tuned for "smoke test a Linux box, then stop it": dispose
// suspend maps to `instances stop` (no compute charges); destroy maps to
// `instances delete --quiet`.
package gcp

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

// Provider implements provider.Provider for GCP Compute Engine.
type Provider struct{}

// New returns a GCP provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "gcp" }

// Doctor verifies gcloud is installed and a project + zone are set.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if _, err := exec.LookPath("gcloud"); err != nil {
		return provider.Health{OK: false, Message: "gcloud CLI not on PATH (install: brew install --cask gcloud-cli)"}
	}
	if project(i) == "" {
		return provider.Health{OK: false, Message: "gcp.project is required"}
	}
	if zone(i) == "" {
		return provider.Health{OK: false, Message: "gcp.zone is required"}
	}
	out, err := p.run(ctx, i, "auth", "list", "--format=json", "--filter=status:ACTIVE")
	if err != nil {
		return provider.Health{OK: false, Message: "gcloud auth list: " + err.Error()}
	}
	var accts []struct {
		Account string `json:"account"`
	}
	_ = json.Unmarshal([]byte(out), &accts)
	if len(accts) == 0 {
		return provider.Health{OK: false, Message: "no active gcloud account (run `gcloud auth login`)"}
	}
	return provider.Health{OK: true, Message: fmt.Sprintf("project=%s zone=%s account=%s", project(i), zone(i), accts[0].Account)}
}

// Status describes the instance and maps gcloud status to vmlab states.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	out, err := p.run(ctx, i, "compute", "instances", "describe", instName(i),
		"--zone", zone(i), "--format=json")
	if err != nil {
		if isNotFoundOutput(out) {
			return provider.StateNotFound, nil
		}
		return provider.StateUnknown, err
	}
	var sd struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &sd); err != nil {
		return provider.StateUnknown, fmt.Errorf("parse describe: %w", err)
	}
	switch strings.ToUpper(sd.Status) {
	case "RUNNING":
		return provider.StateRunning, nil
	case "PROVISIONING", "STAGING":
		return provider.StateStarting, nil
	case "STOPPED", "STOPPING", "SUSPENDED", "SUSPENDING", "TERMINATED":
		return provider.StateStopped, nil
	}
	return provider.StateUnknown, nil
}

// Up creates (or starts) the GCE instance and waits for SSH on its public IP.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	res := provider.EnsureResult{}
	prior, err := p.Status(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	res.PriorState = prior
	switch prior {
	case provider.StateRunning, provider.StateStarting:
		res.Reason = "already running"
	case provider.StateNotFound:
		if err := p.create(ctx, i); err != nil {
			return target.Target{}, res, fmt.Errorf("gcloud instances create: %w", err)
		}
		res.Changed = true
		res.Reason = "created by vmlab"
	case provider.StateStopped:
		if _, err := p.run(ctx, i, "compute", "instances", "start", instName(i), "--zone", zone(i)); err != nil {
			return target.Target{}, res, fmt.Errorf("gcloud instances start: %w", err)
		}
		res.Changed = true
		res.Reason = "started by vmlab"
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
	return buildTarget(i, ip), res, nil
}

// Down disposes per Dispose.
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
	name := instName(i)
	switch d {
	case provider.DisposeSuspend, provider.DisposePowerOff:
		if cur == provider.StateStopped {
			return nil
		}
		_, err := p.run(ctx, i, "compute", "instances", "stop", name, "--zone", zone(i))
		return err
	case provider.DisposeDestroy:
		_, err := p.run(ctx, i, "compute", "instances", "delete", name, "--zone", zone(i), "--quiet")
		return err
	}
	return fmt.Errorf("unknown dispose: %v", d)
}

// create issues `gcloud compute instances create` with the instance's config.
// Labels include vmlab=<name> so future fleet sweeps can find vmlab-owned VMs.
func (p *Provider) create(ctx context.Context, i provider.Instance) error {
	args := []string{"compute", "instances", "create", instName(i),
		"--zone", zone(i),
		"--machine-type", settingOr(i, "machineType", "e2-micro"),
	}
	if img := i.SettingString("gcp", "image"); img != "" {
		args = append(args, "--image", img)
	} else {
		// image-family is more stable than pinned image names.
		args = append(args, "--image-family", settingOr(i, "imageFamily", "debian-12"))
		args = append(args, "--image-project", settingOr(i, "imageProject", "debian-cloud"))
	}
	if net := i.SettingString("gcp", "network"); net != "" {
		args = append(args, "--network", net)
	}
	if subnet := i.SettingString("gcp", "subnet"); subnet != "" {
		args = append(args, "--subnet", subnet)
	}
	if t := i.SettingString("gcp", "tags"); t != "" {
		args = append(args, "--tags", t)
	}
	if ud := i.SettingString("gcp", "userDataFile"); ud != "" {
		args = append(args, "--metadata-from-file", "startup-script="+ud)
	}
	args = append(args, "--labels", "vmlab="+i.Name)
	_, err := p.run(ctx, i, args...)
	return err
}

// publicIP fetches the external IP of the first network interface.
func (p *Provider) publicIP(ctx context.Context, i provider.Instance) (string, error) {
	out, err := p.run(ctx, i, "compute", "instances", "describe", instName(i),
		"--zone", zone(i),
		"--format=value(networkInterfaces[0].accessConfigs[0].natIP)")
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", errors.New("gcp: instance has no external IP")
	}
	return ip, nil
}

// run shells out to gcloud with --project/--account honoured from settings.
func (p *Provider) run(ctx context.Context, i provider.Instance, args ...string) (string, error) {
	prefix := []string{}
	if pr := project(i); pr != "" {
		prefix = append(prefix, "--project", pr)
	}
	if acct := i.SettingString("gcp", "account"); acct != "" {
		prefix = append(prefix, "--account", acct)
	}
	args = append(prefix, args...)
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("gcloud exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

func project(i provider.Instance) string { return i.SettingString("gcp", "project") }
func zone(i provider.Instance) string    { return i.SettingString("gcp", "zone") }
func instName(i provider.Instance) string {
	if n := i.SettingString("gcp", "name"); n != "" {
		return n
	}
	return i.Name
}

func settingOr(i provider.Instance, key, fallback string) string {
	if v := i.SettingString("gcp", key); v != "" {
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

func buildTarget(i provider.Instance, ip string) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "ssh"
	}
	settings := map[string]any{
		"ssh": map[string]any{
			"host":       ip,
			"user":       settingOr(i, "user", os.Getenv("USER")),
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

// WaitReady polls TCP :22 on the external IP. Implements provider.ReadyWaiter.
func (p *Provider) WaitReady(ctx context.Context, i provider.Instance) error {
	ip, err := p.publicIP(ctx, i)
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

func isNotFoundOutput(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "not found") || strings.Contains(s, "was not found") || strings.Contains(s, "no such")
}
