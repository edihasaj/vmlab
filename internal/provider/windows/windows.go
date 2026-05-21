// Package windows is a thin provider for already-provisioned Windows hosts
// (bare-metal labs, externally-managed Hyper-V VMs, anything the user
// doesn't want vmlab to power-cycle). It emits a target pointing at the
// ssh-windows transport and reports lifecycle state as a function of
// reachability — never powering the box itself.
//
// For cloud Windows (Azure / EC2 / GCP), keep using the matching cloud
// provider and set `target.transport: ssh-windows` on the instance — the
// cloud provider already handles power-state; this one would just get in
// the way.
//
// For Windows on Parallels (Mac host), keep using the `parallels` provider:
// it already drives the VM lifecycle. Set `target.transport: ssh-windows`
// if you want to bypass `prlctl exec` in favour of OpenSSH-on-Windows.
package windows

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// Provider implements provider.Provider for static Windows hosts.
type Provider struct{}

// New returns a Windows provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "windows" }

// Doctor delegates to the ssh-windows transport doctor on a synthesized
// target so we surface the same diagnostics callers would see at run time.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if host := i.SettingString("ssh", "host"); host == "" {
		return provider.Health{OK: false, Message: "ssh.host is required (no power-state, just reach)"}
	}
	t := buildTarget(i)
	h := transport.NewSSHWindows().Doctor(ctx, t)
	return provider.Health{OK: h.OK, Message: h.Message, Details: h.Details}
}

// Status maps reachability to lifecycle state. The Windows provider never
// owns power-state, so the result is binary: StateReady when ssh-windows
// can talk to it, StateNotFound otherwise. This is honest — if the user
// wants real power-state, they should be on a cloud / hypervisor provider.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	if i.SettingString("ssh", "host") == "" {
		return provider.StateUnknown, errors.New("ssh.host is required")
	}
	t := buildTarget(i)
	h := transport.NewSSHWindows().Doctor(ctx, t)
	if h.OK {
		return provider.StateReady, nil
	}
	return provider.StateNotFound, nil
}

// Up is a no-op verification step: it asserts the box is reachable and
// returns the ssh-windows target. EnsureResult.Changed is always false —
// vmlab does not own this lifecycle, so cleanup hooks must not assume they
// can power it down.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	t := buildTarget(i)
	res := provider.EnsureResult{PriorState: provider.StateReady}
	h := transport.NewSSHWindows().Doctor(ctx, t)
	if !h.OK {
		res.PriorState = provider.StateNotFound
		return t, res, fmt.Errorf("windows: %s", h.Message)
	}
	res.Reason = "already reachable"
	return t, res, nil
}

// Down is a no-op for DisposeKeep (the only sensible default for an
// externally-managed box). Any other disposition is rejected so users
// don't accidentally try to suspend / destroy something we don't own.
func (p *Provider) Down(ctx context.Context, i provider.Instance, d provider.Dispose) error {
	if d == provider.DisposeKeep {
		return nil
	}
	return fmt.Errorf("windows provider: disposition %s not supported (externally-managed box; configure disposition.on_success=keep)", d)
}

// buildTarget assembles the ssh-windows target. All ssh.* settings on the
// instance pass straight through.
func buildTarget(i provider.Instance) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "ssh-windows"
	}
	settings := map[string]any{}
	// Forward the entire ssh sub-tree if the user set it as a map.
	if raw := i.Setting("ssh"); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			cp := make(map[string]any, len(m))
			for k, v := range m {
				cp[k] = v
			}
			// Default to Administrator if user is unset — Windows OpenSSH
			// users almost always want an admin account, and "root" (the
			// transport.ssh default) doesn't exist.
			if u, _ := cp["user"].(string); strings.TrimSpace(u) == "" {
				cp["user"] = "Administrator"
			}
			settings["ssh"] = cp
		}
	}
	if _, ok := settings["ssh"]; !ok {
		settings["ssh"] = map[string]any{"user": "Administrator"}
	}
	return target.Target{
		Name:      i.Name,
		Transport: tt,
		Tags:      i.Tags,
		Settings:  settings,
	}
}
