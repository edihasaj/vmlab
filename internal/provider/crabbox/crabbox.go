// Package crabbox implements lifecycle management for Crabbox leases.
package crabbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
)

// Provider implements provider.Provider by delegating lease ownership to the
// installed Crabbox CLI.
type Provider struct{}

// New returns a Crabbox lifecycle provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "crabbox" }

type statusView struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Provider string `json:"provider"`
	State    string `json:"state"`
	Ready    bool   `json:"ready"`
}

type leaseState struct {
	ID       string `json:"id"`
	Slug     string `json:"slug,omitempty"`
	Provider string `json:"provider"`
}

var leasedLine = regexp.MustCompile(`(?m)^leased\s+(\S+)(?:\s+slug=(\S+))?`)

// Doctor confirms the CLI and selected backend are usable without creating a lease.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if _, err := exec.LookPath("crabbox"); err != nil {
		return provider.Health{OK: false, Message: "crabbox CLI not on PATH (install: brew install openclaw/tap/crabbox)"}
	}
	backend, err := backendFor(i)
	if err != nil {
		return provider.Health{OK: false, Message: err.Error()}
	}
	if _, err := p.run(ctx, "list", "--provider", backend, "--json"); err != nil {
		return provider.Health{OK: false, Message: err.Error(), Details: map[string]string{"backend": backend}}
	}
	return provider.Health{OK: true, Message: "crabbox ready", Details: map[string]string{"backend": backend}}
}

// Status reports the configured or last vmlab-created lease.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	v, found, err := p.lookup(ctx, i)
	if err != nil {
		return provider.StateUnknown, err
	}
	if !found {
		return provider.StateNotFound, nil
	}
	return mapState(v), nil
}

// Up adopts an existing ready lease or warms a new one, then emits a Crabbox target.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	res := provider.EnsureResult{PriorState: provider.StateNotFound}
	backend, err := backendFor(i)
	if err != nil {
		return target.Target{}, res, err
	}
	v, found, err := p.lookup(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	if found {
		res.PriorState = mapState(v)
		if res.PriorState == provider.StateReady || res.PriorState == provider.StateRunning {
			res.Reason = "crabbox lease already ready"
			return buildTarget(i, v, backend), res, nil
		}
		if res.PriorState == provider.StateStarting {
			ref := v.ID
			if ref == "" {
				ref = v.Slug
			}
			v, err = p.waitStatus(ctx, backend, ref, readyTimeout(i))
			if err != nil {
				return target.Target{}, res, err
			}
			res.Reason = "waited for existing crabbox lease"
			return buildTarget(i, v, backend), res, nil
		}
	}

	args, err := warmupArgs(i, backend)
	if err != nil {
		return target.Target{}, res, err
	}
	out, err := p.run(ctx, args...)
	if err != nil {
		return target.Target{}, res, err
	}
	created, err := parseLease(out, backend)
	if err != nil {
		return target.Target{}, res, err
	}
	if err := writeState(i, created); err != nil {
		_, _ = p.run(ctx, "stop", "--provider", backend, created.ID)
		return target.Target{}, res, err
	}
	v, found, err = p.status(ctx, backend, created.ID)
	if err != nil || !found || (mapState(v) != provider.StateReady && mapState(v) != provider.StateRunning) {
		_, _ = p.run(ctx, "stop", "--provider", backend, created.ID)
		_ = removeState(i)
		if err != nil {
			return target.Target{}, res, fmt.Errorf("crabbox lease readiness: %w", err)
		}
		return target.Target{}, res, fmt.Errorf("crabbox lease %s not ready, state=%s", created.ID, v.State)
	}
	res.Changed = true
	res.Reason = "warmed crabbox " + backend + " lease by vmlab"
	return buildTarget(i, v, backend), res, nil
}

// Down releases a vmlab-managed Crabbox lease. Crabbox has no suspend state,
// so every non-keep disposition maps to stop/release.
func (p *Provider) Down(ctx context.Context, i provider.Instance, d provider.Dispose) error {
	if d == provider.DisposeKeep {
		return nil
	}
	backend, err := backendFor(i)
	if err != nil {
		return err
	}
	v, found, err := p.lookup(ctx, i)
	if err != nil {
		return err
	}
	if !found {
		return removeState(i)
	}
	ref := v.ID
	if ref == "" {
		ref = v.Slug
	}
	if _, err := p.run(ctx, "stop", "--provider", backend, ref); err != nil {
		if !isNotFound(err.Error()) {
			return err
		}
	}
	return removeState(i)
}

func (p *Provider) lookup(ctx context.Context, i provider.Instance) (statusView, bool, error) {
	backend, err := backendFor(i)
	if err != nil {
		return statusView{}, false, err
	}
	refs := []string{i.SettingString("crabbox", "id")}
	if s, err := readState(i); err == nil {
		refs = append(refs, s.ID, s.Slug)
	}
	refs = append(refs, i.SettingString("crabbox", "slug"))
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		v, found, err := p.status(ctx, backend, ref)
		if err != nil || found {
			return v, found, err
		}
	}
	return statusView{}, false, nil
}

func (p *Provider) status(ctx context.Context, backend, ref string) (statusView, bool, error) {
	out, err := p.run(ctx, "status", "--provider", backend, "--id", ref, "--json")
	if err != nil {
		if isNotFound(err.Error()) {
			return statusView{}, false, nil
		}
		return statusView{}, false, err
	}
	var v statusView
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return statusView{}, false, fmt.Errorf("parse crabbox status: %w", err)
	}
	return v, true, nil
}

func (p *Provider) waitStatus(ctx context.Context, backend, ref, timeout string) (statusView, error) {
	out, err := p.run(ctx, "status", "--provider", backend, "--id", ref, "--wait", "--wait-timeout", timeout, "--json")
	if err != nil {
		return statusView{}, fmt.Errorf("wait for crabbox lease %s: %w", ref, err)
	}
	var v statusView
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		return statusView{}, fmt.Errorf("parse crabbox status: %w", err)
	}
	if state := mapState(v); state != provider.StateReady && state != provider.StateRunning {
		return statusView{}, fmt.Errorf("crabbox lease %s not ready, state=%s", ref, v.State)
	}
	return v, nil
}

func (p *Provider) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "crabbox", args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("crabbox %s exit=%d: %s", args[0], ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, fmt.Errorf("crabbox %s: %w", args[0], err)
	}
	return out, nil
}

func backendFor(i provider.Instance) (string, error) {
	if v := i.SettingString("crabbox", "provider"); v != "" {
		return v, nil
	}
	guest := targetOS(i)
	if guest == "linux" {
		return "local-container", nil
	}
	hint := "set crabbox.provider explicitly"
	if runtime.GOOS == "darwin" {
		hint += " (for example parallels or tart)"
	} else if runtime.GOOS == "windows" {
		hint += " (for example hyperv or windows-sandbox)"
	}
	return "", fmt.Errorf("crabbox: no safe automatic backend for target %q on host %s, %s", guest, runtime.GOOS, hint)
}

func targetOS(i provider.Instance) string {
	if v := strings.ToLower(i.SettingString("crabbox", "target")); v != "" {
		return v
	}
	if v := strings.ToLower(i.SettingString("os")); v != "" {
		if v == "darwin" || v == "mac" {
			return "macos"
		}
		return v
	}
	if i.HasTag("windows") {
		return "windows"
	}
	if i.HasTag("mac") || i.HasTag("macos") {
		return "macos"
	}
	return "linux"
}

func readyTimeout(i provider.Instance) string {
	if i.Ready.Timeout != "" {
		return i.Ready.Timeout
	}
	return "5m"
}

func warmupArgs(i provider.Instance, backend string) ([]string, error) {
	args := []string{"warmup", "--provider", backend, "--target", targetOS(i)}
	slug := i.SettingString("crabbox", "slug")
	if slug == "" {
		slug = i.Name
	}
	if slug != "" {
		args = append(args, "--slug", slug)
	}
	for _, key := range []string{"ttl", "idle-timeout"} {
		setting := strings.ReplaceAll(key, "-", "")
		if key == "idle-timeout" {
			setting = "idleTimeout"
		}
		if v := i.SettingString("crabbox", setting); v != "" {
			args = append(args, "--"+key, v)
		}
	}
	for _, key := range []string{"desktop", "browser", "code"} {
		if boolSetting(i.Setting("crabbox", key)) {
			args = append(args, "--"+key)
		}
	}
	if backend == "local-container" {
		for _, pair := range [][2]string{{"runtime", "runtime"}, {"image", "image"}, {"user", "user"}, {"workRoot", "work-root"}, {"cpus", "cpus"}, {"memory", "memory"}, {"network", "network"}} {
			if v := settingText(i.Setting("crabbox", "localContainer", pair[0])); v != "" {
				args = append(args, "--local-container-"+pair[1], v)
			}
		}
		if boolSetting(i.Setting("crabbox", "localContainer", "dockerSocket")) {
			args = append(args, "--local-container-docker-socket")
		}
		if values, ok := i.Setting("crabbox", "localContainer", "volumes").([]any); ok {
			for _, v := range values {
				args = append(args, "--local-container-volume", fmt.Sprint(v))
			}
		}
	}
	return args, nil
}

func parseLease(out, backend string) (statusView, error) {
	m := leasedLine.FindStringSubmatch(out)
	if len(m) == 0 {
		return statusView{}, fmt.Errorf("parse crabbox warmup: missing leased line in %q", strings.TrimSpace(out))
	}
	return statusView{ID: m[1], Slug: m[2], Provider: backend}, nil
}

func mapState(v statusView) provider.State {
	if v.Ready {
		return provider.StateReady
	}
	switch strings.ToLower(v.State) {
	case "ready":
		return provider.StateReady
	case "running", "active", "leased":
		return provider.StateRunning
	case "creating", "provisioning", "starting", "warming":
		return provider.StateStarting
	case "stopped", "released", "expired", "terminated", "missing":
		return provider.StateStopped
	default:
		return provider.StateUnknown
	}
}

func buildTarget(i provider.Instance, v statusView, backend string) target.Target {
	ref := v.ID
	if ref == "" {
		ref = v.Slug
	}
	return target.Target{
		Name: i.Name, Transport: "crabbox", Tags: append([]string(nil), i.Tags...),
		Caps:     target.Caps{Shell: true, Sync: true, Install: true, Screenshot: boolSetting(i.Setting("crabbox", "desktop"))},
		Settings: map[string]any{"os": targetOS(i), "crabbox": map[string]any{"id": ref, "provider": backend}},
	}
}

func statePath(i provider.Instance) (string, error) {
	p, err := config.ResolvePaths()
	if err != nil {
		return "", err
	}
	safe := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(i.Name, "_")
	return filepath.Join(p.StateDir, "crabbox", safe+".json"), nil
}

func writeState(i provider.Instance, v statusView) error {
	path, err := statePath(i)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(leaseState{ID: v.ID, Slug: v.Slug, Provider: v.Provider})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func readState(i provider.Instance) (leaseState, error) {
	path, err := statePath(i)
	if err != nil {
		return leaseState{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return leaseState{}, err
	}
	var s leaseState
	err = json.Unmarshal(b, &s)
	return s, err
}

func removeState(i provider.Instance) error {
	path, err := statePath(i)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func boolSetting(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	default:
		return false
	}
}

func settingText(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func isNotFound(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "not found") || strings.Contains(s, "no lease")
}
