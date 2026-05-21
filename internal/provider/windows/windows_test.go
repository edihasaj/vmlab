package windows

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
)

// stubSSH writes a fake ssh binary that always exits 0 and prepends its
// directory to PATH. Lets us exercise Status / Up without a real network.
func stubSSH(t *testing.T, exit int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub uses POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit "+itoa(exit)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}

func TestStatusReachable(t *testing.T) {
	stubSSH(t, 0)
	p := New()
	i := provider.Instance{
		Name:     "win11",
		Provider: "windows",
		Settings: map[string]any{
			"ssh": map[string]any{"host": "win11.lan"},
		},
	}
	st, err := p.Status(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if st != provider.StateReady {
		t.Fatalf("expected StateReady, got %s", st)
	}
}

func TestStatusUnreachable(t *testing.T) {
	stubSSH(t, 1)
	p := New()
	i := provider.Instance{
		Name:     "win11",
		Provider: "windows",
		Settings: map[string]any{
			"ssh": map[string]any{"host": "win11.lan"},
		},
	}
	st, err := p.Status(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if st != provider.StateNotFound {
		t.Fatalf("expected StateNotFound, got %s", st)
	}
}

func TestUpReturnsSshWindowsTarget(t *testing.T) {
	stubSSH(t, 0)
	p := New()
	i := provider.Instance{
		Name:     "win11",
		Provider: "windows",
		Settings: map[string]any{
			"ssh": map[string]any{"host": "win11.lan"},
		},
	}
	tgt, res, err := p.Up(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Transport != "ssh-windows" {
		t.Errorf("expected ssh-windows transport, got %s", tgt.Transport)
	}
	if res.Changed {
		t.Errorf("windows provider must never claim Changed=true")
	}
	// Defaults: user falls back to Administrator.
	if u := tgt.SettingString("ssh", "user"); u != "Administrator" {
		t.Errorf("expected default user Administrator, got %q", u)
	}
}

func TestUpFailsWhenUnreachable(t *testing.T) {
	stubSSH(t, 1)
	p := New()
	i := provider.Instance{
		Name:     "win11",
		Provider: "windows",
		Settings: map[string]any{
			"ssh": map[string]any{"host": "win11.lan"},
		},
	}
	_, _, err := p.Up(context.Background(), i)
	if err == nil {
		t.Fatal("expected unreachable error")
	}
}

func TestDownRejectsNonKeep(t *testing.T) {
	p := New()
	i := provider.Instance{Name: "win11"}
	if err := p.Down(context.Background(), i, provider.DisposeKeep); err != nil {
		t.Fatalf("DisposeKeep must be a no-op, got %v", err)
	}
	if err := p.Down(context.Background(), i, provider.DisposeDestroy); err == nil {
		t.Fatal("DisposeDestroy must be rejected")
	}
	if err := p.Down(context.Background(), i, provider.DisposePowerOff); err == nil {
		t.Fatal("DisposePowerOff must be rejected")
	}
}

func TestDoctorMissingHost(t *testing.T) {
	p := New()
	h := p.Doctor(context.Background(), provider.Instance{Name: "x"})
	if h.OK {
		t.Fatal("expected unhealthy without ssh.host")
	}
}
