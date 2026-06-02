package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
)

func TestInstanceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	userInstances := filepath.Join(dir, "user", "instances")
	if err := os.MkdirAll(userInstances, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`name: win11-studio
provider: parallels
tags: [windows]
parallels:
  host: mac-studio.local
  vm: Windows 11
ready:
  kind: parallels-tools
  timeout: 120s
target:
  transport: parallels-guest
disposition:
  on_success: suspend
  on_failure: suspend
  only_if_we_started: true
`)
	if err := os.WriteFile(filepath.Join(userInstances, "win11-studio.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	p := config.Paths{
		InstanceDir: []string{userInstances},
	}
	r, err := LoadInstances(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := r.Get("win11-studio")
	if !ok {
		t.Fatalf("instance not found")
	}
	if got.Provider != "parallels" {
		t.Errorf("provider=%q", got.Provider)
	}
	if got.SettingString("parallels", "host") != "mac-studio.local" {
		t.Errorf("host=%q", got.SettingString("parallels", "host"))
	}
	if got.Ready.Kind != "parallels-tools" {
		t.Errorf("ready.kind=%q", got.Ready.Kind)
	}
	if got.Target.Transport != "parallels-guest" {
		t.Errorf("target.transport=%q", got.Target.Transport)
	}
	if got.Disp.OnSuccess != "suspend" {
		t.Errorf("disposition.on_success=%q", got.Disp.OnSuccess)
	}
	if !got.Disp.OnlyIfWeStarted {
		t.Errorf("disposition.only_if_we_started not set")
	}
}

func TestInstanceMissingProviderRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := config.Paths{InstanceDir: []string{dir}}
	if _, err := LoadInstances(p); err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestParseDispose(t *testing.T) {
	cases := map[string]Dispose{
		"":         DisposeKeep,
		"keep":     DisposeKeep,
		"suspend":  DisposeSuspend,
		"poweroff": DisposePowerOff,
		"stop":     DisposePowerOff,
		"destroy":  DisposeDestroy,
	}
	for in, want := range cases {
		got, err := ParseDispose(in)
		if err != nil {
			t.Errorf("ParseDispose(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDispose(%q)=%v, want %v", in, got, want)
		}
	}
	if _, err := ParseDispose("vaporize"); err == nil {
		t.Errorf("expected error for unknown dispose")
	}
}

func TestRegistryRegisterDuplicatePanics(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubProv{name: "x"})
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on duplicate registration")
		}
	}()
	r.Register(&stubProv{name: "x"})
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("missing"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

type stubProv struct{ name string }

func (s *stubProv) Name() string                                { return s.name }
func (s *stubProv) Doctor(_ context.Context, _ Instance) Health { return Health{OK: true} }
func (s *stubProv) Status(_ context.Context, _ Instance) (State, error) {
	return StateUnknown, nil
}
func (s *stubProv) Up(_ context.Context, _ Instance) (target.Target, EnsureResult, error) {
	return target.Target{}, EnsureResult{}, nil
}
func (s *stubProv) Down(_ context.Context, _ Instance, _ Dispose) error { return nil }
