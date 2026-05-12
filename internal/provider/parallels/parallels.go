// Package parallels implements the Parallels Desktop provider. Lifecycle
// (Status / Up / Down) is driven by `prlctl` on the host that owns the VM;
// when the host is remote, prlctl is invoked over SSH. Tools-readiness is
// detected by polling `prlctl exec` with a no-op command — the provider
// owns this loop so callers don't repeat it.
package parallels

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
)

// Provider implements provider.Provider for Parallels.
type Provider struct{}

// New returns a Parallels provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "parallels" }

// Doctor checks that prlctl is reachable for the instance.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	vm := i.SettingString("parallels", "vm")
	if vm == "" {
		return provider.Health{OK: false, Message: "parallels.vm is required"}
	}
	out, err := p.runPrlctl(ctx, i, "--version")
	if err != nil {
		return provider.Health{OK: false, Message: err.Error()}
	}
	return provider.Health{
		OK:      true,
		Message: strings.TrimSpace(out),
		Details: map[string]string{"vm": vm},
	}
}

// Status returns the current state of the VM.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	vm := i.SettingString("parallels", "vm")
	if vm == "" {
		return provider.StateUnknown, errors.New("parallels.vm is required")
	}
	out, err := p.runPrlctl(ctx, i, "status", vm)
	if err != nil {
		// `prlctl status <name>` returns non-zero when the VM does not exist.
		if isNotFoundOutput(out) {
			return provider.StateNotFound, nil
		}
		return provider.StateUnknown, err
	}
	return parseStatus(out), nil
}

// Up brings the VM to running (and waits for ready if the instance asks).
// Returns the Target the transport layer should use, plus EnsureResult so
// callers can decide whether they "own" the running state.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	t := buildTarget(i)
	prior, err := p.Status(ctx, i)
	if err != nil {
		return t, provider.EnsureResult{PriorState: prior}, err
	}
	res := provider.EnsureResult{PriorState: prior}
	vm := i.SettingString("parallels", "vm")
	switch prior {
	case provider.StateRunning, provider.StateReady:
		// already running. tools-ready poll still happens below.
		res.Reason = "already running"
	case provider.StateSuspended, provider.StateStopped:
		if _, err := p.runPrlctl(ctx, i, "start", vm); err != nil {
			return t, res, fmt.Errorf("prlctl start: %w", err)
		}
		res.Changed = true
		res.Reason = "started by vmlab"
	case provider.StateNotFound:
		return t, res, fmt.Errorf("vm %q not found on host", vm)
	default:
		return t, res, fmt.Errorf("unexpected prior state: %s", prior)
	}
	if err := p.waitReady(ctx, i); err != nil {
		return t, res, err
	}
	return t, res, nil
}

// Down disposes of the VM per the requested Dispose. Idempotent.
func (p *Provider) Down(ctx context.Context, i provider.Instance, d provider.Dispose) error {
	vm := i.SettingString("parallels", "vm")
	if vm == "" {
		return errors.New("parallels.vm is required")
	}
	if d == provider.DisposeKeep {
		return nil
	}
	cur, err := p.Status(ctx, i)
	if err != nil {
		return err
	}
	switch d {
	case provider.DisposeKeep:
		return nil
	case provider.DisposeSuspend:
		if cur == provider.StateSuspended || cur == provider.StateStopped || cur == provider.StateNotFound {
			return nil
		}
		_, err := p.runPrlctl(ctx, i, "suspend", vm)
		return err
	case provider.DisposePowerOff:
		if cur == provider.StateStopped || cur == provider.StateNotFound {
			return nil
		}
		_, err := p.runPrlctl(ctx, i, "stop", vm)
		return err
	case provider.DisposeDestroy:
		if cur == provider.StateNotFound {
			return nil
		}
		if cur != provider.StateStopped {
			_, _ = p.runPrlctl(ctx, i, "stop", vm, "--kill")
		}
		_, err := p.runPrlctl(ctx, i, "delete", vm)
		return err
	}
	return fmt.Errorf("unknown dispose: %v", d)
}

// waitReady polls `prlctl exec <vm> <probe>` until it succeeds or times out.
// Probe defaults are platform-shaped: cmd.exe for Windows, true for *nix.
func (p *Provider) waitReady(ctx context.Context, i provider.Instance) error {
	timeout := 120 * time.Second
	if i.Ready.Timeout != "" {
		if d, err := time.ParseDuration(i.Ready.Timeout); err == nil {
			timeout = d
		}
	}
	probe := []string{"cmd.exe", "/c", "ver"}
	if alt := i.SettingString("parallels", "readyProbe"); alt != "" {
		probe = strings.Fields(alt)
	}
	vm := i.SettingString("parallels", "vm")
	deadline := time.Now().Add(timeout)
	delay := time.Second
	for {
		args := append([]string{"exec", vm}, probe...)
		_, err := p.runPrlctl(ctx, i, args...)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("waitReady: timed out after %s: %w", timeout, err)
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

// runPrlctl invokes prlctl with args, locally or via SSH depending on the
// instance config. Returns combined stdout+stderr for diagnostic parsing.
func (p *Provider) runPrlctl(ctx context.Context, i provider.Instance, args ...string) (string, error) {
	host := i.SettingString("parallels", "host")
	prlPath := i.SettingString("parallels", "prlctlPath")
	if prlPath == "" {
		prlPath = "/Applications/Parallels Desktop.app/Contents/MacOS"
	}
	var cmd *exec.Cmd
	if host == "" {
		bin := "prlctl"
		if alt := i.SettingString("parallels", "bin"); alt != "" {
			bin = alt
		}
		cmd = exec.CommandContext(ctx, bin, args...)
	} else {
		sshArgs := []string{"-o", "ConnectTimeout=8", "-o", "BatchMode=yes", "-o", "RequestTTY=no"}
		if port := i.SettingString("parallels", "port"); port != "" {
			sshArgs = append(sshArgs, "-p", port)
		}
		dest := host
		if user := i.SettingString("parallels", "user"); user != "" {
			dest = user + "@" + host
		}
		sshArgs = append(sshArgs, dest, "--")
		quoted := make([]string, 0, len(args))
		for _, a := range args {
			quoted = append(quoted, posixQuote(a))
		}
		remote := fmt.Sprintf("PATH=\"$PATH:%s\" prlctl %s", prlPath, strings.Join(quoted, " "))
		sshArgs = append(sshArgs, remote)
		cmd = exec.CommandContext(ctx, "ssh", sshArgs...)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("prlctl exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

// buildTarget assembles the Target a transport layer will use to talk to
// the running VM. Defaults to parallels-guest with the same host+vm.
func buildTarget(i provider.Instance) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "parallels-guest"
	}
	settings := map[string]any{
		"parallels": map[string]any{
			"host": i.SettingString("parallels", "host"),
			"vm":   i.SettingString("parallels", "vm"),
		},
	}
	if user := i.SettingString("parallels", "user"); user != "" {
		settings["parallels"].(map[string]any)["user"] = user
	}
	if port := i.SettingString("parallels", "port"); port != "" {
		settings["parallels"].(map[string]any)["port"] = port
	}
	return target.Target{
		Name:      i.Name,
		Transport: tt,
		Tags:      i.Tags,
		Settings:  settings,
	}
}

// parseStatus parses `prlctl status <vm>` output. Example:
//
//	VM "Windows 11" exist running
//	VM "Windows 11" exist suspended
func parseStatus(out string) provider.State {
	out = strings.ToLower(out)
	switch {
	case strings.Contains(out, "running"):
		return provider.StateRunning
	case strings.Contains(out, "suspended"), strings.Contains(out, "paused"):
		return provider.StateSuspended
	case strings.Contains(out, "stopped"):
		return provider.StateStopped
	case strings.Contains(out, "starting"):
		return provider.StateStarting
	}
	return provider.StateUnknown
}

func isNotFoundOutput(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "not found") || strings.Contains(s, "no such vm") || strings.Contains(s, "invalid usage")
}

// posixQuote wraps s in single quotes for a POSIX shell, escaping embedded
// single quotes. Mirrors the helper in internal/transport.
func posixQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r\"'\\$`*?[]{}|&;<>()#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

