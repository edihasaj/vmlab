package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
	_ "github.com/edihasaj/vmlab/internal/provider/all"
)

func stubHcloudJSON(t *testing.T, json string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
cat <<'JSON'
%s
JSON
`, json)
	if err := os.WriteFile(filepath.Join(dir, "hcloud"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// hetznerSweeper grabs the registered hetzner provider as an OrphanSweeper.
func hetznerSweeper(t *testing.T) provider.OrphanSweeper {
	t.Helper()
	p, err := provider.Default().Get("hetzner")
	if err != nil {
		t.Fatalf("hetzner provider not registered: %v", err)
	}
	sw, ok := p.(provider.OrphanSweeper)
	if !ok {
		t.Fatalf("hetzner provider not an OrphanSweeper")
	}
	return sw
}

func TestHetznerOrphansEmpty(t *testing.T) {
	stubHcloudJSON(t, "[]")
	orphans, err := hetznerSweeper(t).ListOrphans(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans, got %d: %+v", len(orphans), orphans)
	}
}

func TestHetznerOrphansParses(t *testing.T) {
	stubHcloudJSON(t, `[
  {"name":"smoke-1","status":"running","labels":{"vmlab":"run-abc"}},
  {"name":"unrelated","status":"running","labels":{"team":"sre"}},
  {"name":"smoke-2","status":"off","labels":{"vmlab":"run-def","app":"x"}}
]`)
	orphans, err := hetznerSweeper(t).ListOrphans(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphans, got %d: %+v", len(orphans), orphans)
	}
	want := map[string]string{"smoke-1": "vmlab=run-abc", "smoke-2": "vmlab=run-def"}
	for _, o := range orphans {
		if w, ok := want[o.Name]; !ok {
			t.Errorf("unexpected orphan: %+v", o)
		} else if o.Label != w {
			t.Errorf("label=%q, want %q", o.Label, w)
		}
	}
}

func TestHetznerOrphansNoBinary(t *testing.T) {
	// PATH with no hcloud is fine — sweep is a no-op rather than an error.
	t.Setenv("PATH", t.TempDir())
	orphans, err := hetznerSweeper(t).ListOrphans(context.Background())
	if err != nil {
		t.Fatalf("expected nil err, got: %v", err)
	}
	if orphans != nil {
		t.Errorf("expected nil orphans, got: %+v", orphans)
	}
}
