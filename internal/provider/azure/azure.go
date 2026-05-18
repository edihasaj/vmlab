// Package azure implements the Microsoft Azure provider. Lifecycle is driven
// by the `az` CLI (no SDK lock-in) and the resulting box is exposed to the
// rest of vmlab via the `ssh` transport.
//
// Defaults are tuned for the "smoke test a Linux box, then deallocate it"
// case: dispose.on_success = suspend (Azure's `vm deallocate`, no compute
// charges) — explicit `destroy` is needed for full teardown.
package azure

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

// Provider implements provider.Provider for Microsoft Azure VMs.
type Provider struct{}

// New returns an Azure provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "azure" }

// Doctor checks that az is on PATH and a session/subscription is reachable.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if _, err := exec.LookPath("az"); err != nil {
		return provider.Health{OK: false, Message: "az CLI not on PATH (install: brew install azure-cli)"}
	}
	if i.SettingString("azure", "resourceGroup") == "" {
		return provider.Health{OK: false, Message: "azure.resourceGroup is required"}
	}
	out, err := p.run(ctx, i, "account", "show", "-o", "json")
	if err != nil {
		return provider.Health{OK: false, Message: "az account show: " + err.Error()}
	}
	var acc struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &acc); err != nil {
		return provider.Health{OK: false, Message: "az account show: " + err.Error()}
	}
	return provider.Health{OK: true, Message: fmt.Sprintf("subscription=%s (%s)", acc.Name, acc.ID)}
}

// Status describes the VM. Azure's powerState comes back as "VM running",
// "VM deallocated", "VM stopped", "VM starting" — we map those to our enum.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	name := vmName(i)
	if name == "" {
		return provider.StateUnknown, errors.New("instance name required")
	}
	out, err := p.run(ctx, i, "vm", "show", "-g", rg(i), "-n", name, "-d", "-o", "json")
	if err != nil {
		if isNotFoundOutput(out) {
			return provider.StateNotFound, nil
		}
		return provider.StateUnknown, err
	}
	var sd struct {
		PowerState string `json:"powerState"`
	}
	if err := json.Unmarshal([]byte(out), &sd); err != nil {
		return provider.StateUnknown, fmt.Errorf("parse vm show: %w", err)
	}
	switch strings.ToLower(strings.TrimPrefix(sd.PowerState, "VM ")) {
	case "running":
		return provider.StateRunning, nil
	case "starting":
		return provider.StateStarting, nil
	case "deallocated", "deallocating", "stopped", "stopping":
		return provider.StateStopped, nil
	}
	return provider.StateUnknown, nil
}

// Up creates (or starts) the VM and waits for SSH on its public IP.
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
			return target.Target{}, res, fmt.Errorf("az vm create: %w", err)
		}
		res.Changed = true
		res.Reason = "created by vmlab"
	case provider.StateStopped:
		if _, err := p.run(ctx, i, "vm", "start", "-g", rg(i), "-n", vmName(i)); err != nil {
			return target.Target{}, res, fmt.Errorf("az vm start: %w", err)
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

// Down disposes per Dispose. `suspend` maps to `vm deallocate` (no compute
// charges); `poweroff` maps to `vm stop` (kept billable, rarely useful).
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
	name := vmName(i)
	switch d {
	case provider.DisposeSuspend:
		if cur == provider.StateStopped {
			return nil
		}
		_, err := p.run(ctx, i, "vm", "deallocate", "-g", rg(i), "-n", name, "--no-wait")
		return err
	case provider.DisposePowerOff:
		if cur == provider.StateStopped {
			return nil
		}
		_, err := p.run(ctx, i, "vm", "stop", "-g", rg(i), "-n", name, "--no-wait")
		return err
	case provider.DisposeDestroy:
		_, err := p.run(ctx, i, "vm", "delete", "-g", rg(i), "-n", name, "--yes", "--no-wait")
		return err
	}
	return fmt.Errorf("unknown dispose: %v", d)
}

// create issues `az vm create` with the instance's config.
func (p *Provider) create(ctx context.Context, i provider.Instance) error {
	name := vmName(i)
	args := []string{"vm", "create",
		"-g", rg(i),
		"-n", name,
		"--image", settingOr(i, "image", "Ubuntu2404"),
		"--size", settingOr(i, "size", "Standard_B1s"),
		"--admin-username", settingOr(i, "adminUsername", "vmlab"),
	}
	if loc := i.SettingString("azure", "location"); loc != "" {
		args = append(args, "--location", loc)
	}
	if k := i.SettingString("azure", "sshKey"); k != "" {
		// Accept either a file path or an inline public key. az auto-detects.
		args = append(args, "--ssh-key-values", k)
	} else {
		// Generate one if the user didn't provide — fine for ephemeral boxes.
		args = append(args, "--generate-ssh-keys")
	}
	args = append(args, "--tags", "vmlab="+name)
	if ud := i.SettingString("azure", "userDataFile"); ud != "" {
		args = append(args, "--user-data", "@"+ud)
	}
	_, err := p.run(ctx, i, args...)
	return err
}

// publicIP fetches the VM's first public IPv4. Azure surfaces it on the
// show -d output as `publicIps` (comma-separated when multi-NIC).
func (p *Provider) publicIP(ctx context.Context, i provider.Instance) (string, error) {
	out, err := p.run(ctx, i, "vm", "show", "-g", rg(i), "-n", vmName(i), "-d", "-o", "json")
	if err != nil {
		return "", err
	}
	var sd struct {
		PublicIPs string `json:"publicIps"`
	}
	if err := json.Unmarshal([]byte(out), &sd); err != nil {
		return "", err
	}
	ip := strings.TrimSpace(strings.SplitN(sd.PublicIPs, ",", 2)[0])
	if ip == "" {
		return "", errors.New("azure: VM has no public IP")
	}
	return ip, nil
}

// run shells out to az, inheriting Azure env vars and honouring an instance
// subscription override. Combines stdout+stderr for diagnostic parsing.
func (p *Provider) run(ctx context.Context, i provider.Instance, args ...string) (string, error) {
	if sub := i.SettingString("azure", "subscription"); sub != "" {
		args = append([]string{"--subscription", sub}, args...)
	}
	cmd := exec.CommandContext(ctx, "az", args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("az exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

func rg(i provider.Instance) string     { return i.SettingString("azure", "resourceGroup") }
func vmName(i provider.Instance) string {
	if n := i.SettingString("azure", "vmName"); n != "" {
		return n
	}
	return i.Name
}

func settingOr(i provider.Instance, key, fallback string) string {
	if v := i.SettingString("azure", key); v != "" {
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
	return 240 * time.Second
}

func buildTarget(i provider.Instance, ip string) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "ssh"
	}
	settings := map[string]any{
		"ssh": map[string]any{
			"host":       ip,
			"user":       settingOr(i, "adminUsername", "vmlab"),
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

// WaitReady polls TCP :22 on the VM's public IP until reachable.
// Implements provider.ReadyWaiter so `vmlab wait` works against Azure.
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
	return strings.Contains(s, "resourcenotfound") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "no resource")
}
