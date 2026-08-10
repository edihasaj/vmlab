package crabbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
)

func installFake(t *testing.T, initiallyReady bool) (logPath, marker string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "args.log")
	marker = filepath.Join(dir, "ready")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_LOG"
case "$1" in
  list) printf '[]\n' ;;
  status)
    if [ ! -f "$FAKE_MARKER" ]; then
      echo 'local-container lease not found: demo' >&2
      exit 4
    fi
    printf '{"id":"cbx_123","slug":"demo","provider":"local-container","state":"ready","ready":true}\n'
    ;;
  warmup)
    touch "$FAKE_MARKER"
    printf 'leased cbx_123 slug=demo provider=local-container\nready cbx_123\n'
    ;;
  stop) rm -f "$FAKE_MARKER" ;;
esac
`
	path := filepath.Join(dir, "crabbox")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if initiallyReady {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOG", logPath)
	t.Setenv("FAKE_MARKER", marker)
	t.Setenv("VMLAB_HOME", filepath.Join(dir, "vmlab-home"))
	return logPath, marker
}

func linuxInstance() provider.Instance {
	return provider.Instance{
		Name:     "demo",
		Provider: "crabbox",
		Tags:     []string{"linux", "sandbox"},
		Settings: map[string]any{"crabbox": map[string]any{
			"target":         "linux",
			"ttl":            "45m",
			"idleTimeout":    "10m",
			"localContainer": map[string]any{"cpus": 2, "memory": "4g"},
		}},
	}
}

func TestUpWarmsLocalContainerAndPersistsLease(t *testing.T) {
	logPath, _ := installFake(t, false)
	p := New()
	tgt, res, err := p.Up(context.Background(), linuxInstance())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.PriorState != provider.StateNotFound {
		t.Fatalf("unexpected result: %+v", res)
	}
	if tgt.Transport != "crabbox" || tgt.SettingString("crabbox", "id") != "cbx_123" || tgt.SettingString("crabbox", "provider") != "local-container" {
		t.Fatalf("unexpected target: %+v", tgt)
	}
	if _, err := readState(linuxInstance()); err != nil {
		t.Fatalf("lease state not persisted: %v", err)
	}
	log, _ := os.ReadFile(logPath)
	got := string(log)
	for _, want := range []string{"warmup --provider local-container", "--target linux", "--slug demo", "--ttl 45m", "--idle-timeout 10m", "--local-container-cpus 2", "--local-container-memory 4g"} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "docker-socket") {
		t.Fatalf("unsafe Docker socket enabled by default:\n%s", got)
	}
}

func TestUpAdoptsReadyLeaseWithoutWarmup(t *testing.T) {
	logPath, _ := installFake(t, true)
	i := linuxInstance()
	i.Settings["crabbox"].(map[string]any)["id"] = "demo"
	tgt, res, err := New().Up(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.PriorState != provider.StateReady {
		t.Fatalf("unexpected result: %+v", res)
	}
	if tgt.SettingString("crabbox", "id") != "cbx_123" {
		t.Fatalf("unexpected target: %+v", tgt)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "warmup") {
		t.Fatalf("existing lease was warmed again:\n%s", log)
	}
}

func TestDownReleasesLeaseForSuspend(t *testing.T) {
	logPath, marker := installFake(t, false)
	p := New()
	i := linuxInstance()
	if _, _, err := p.Up(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if err := p.Down(context.Background(), i, provider.DisposeSuspend); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("fake lease was not released")
	}
	if _, err := readState(i); !os.IsNotExist(err) {
		t.Fatalf("state was not removed: %v", err)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "stop --provider local-container cbx_123") {
		t.Fatalf("stop not invoked:\n%s", log)
	}
}

func TestBackendSelectionRequiresExplicitNonLinuxProvider(t *testing.T) {
	i := provider.Instance{Name: "win", Settings: map[string]any{"crabbox": map[string]any{"target": "windows"}}}
	if _, err := backendFor(i); err == nil || !strings.Contains(err.Error(), runtime.GOOS) {
		t.Fatalf("expected machine-aware selection error, got %v", err)
	}
	i.Settings["crabbox"].(map[string]any)["provider"] = "parallels"
	if got, err := backendFor(i); err != nil || got != "parallels" {
		t.Fatalf("explicit backend: got %q, %v", got, err)
	}
}

func TestDoctorChecksSelectedBackend(t *testing.T) {
	logPath, _ := installFake(t, false)
	h := New().Doctor(context.Background(), linuxInstance())
	if !h.OK || h.Details["backend"] != "local-container" {
		t.Fatalf("unexpected health: %+v", h)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "list --provider local-container --json") {
		t.Fatalf("unexpected doctor command:\n%s", log)
	}
}
