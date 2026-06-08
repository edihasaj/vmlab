package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/edihasaj/vmlab/internal/evidence"
)

// writeRun writes a meta.json with the given lifecycle fields into a fake
// runs dir so the usage command has data to aggregate.
func writeRun(t *testing.T, root, id, provider, instance string, exit int, upMs, runMs, downMs int64, age time.Duration) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := evidence.RunMeta{
		ID:        id,
		StartedAt: time.Now().Add(-age),
		ExitCode:  exit,
		Lifecycle: &evidence.LifecycleSummary{
			Instance: instance,
			Provider: provider,
			UpMs:     upMs, RunMs: runMs, DownMs: downMs,
		},
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUsageAggregatesByInstance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")
	runsDir := filepath.Join(home, ".vmlab", "runs")
	writeRun(t, runsDir, "r1", "hetzner", "linux-a", 0, 1000, 5000, 200, 0)
	writeRun(t, runsDir, "r2", "hetzner", "linux-a", 1, 1000, 200, 100, 0)
	writeRun(t, runsDir, "r3", "azure", "win-b", 0, 1500, 800, 300, 0)

	cmd := newUsageCmd()
	cmd.SetArgs([]string{"--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []usageRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v: %s", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	// Higher-total row first.
	if rows[0].Instance != "linux-a" {
		t.Errorf("expected linux-a first (higher total): got %+v", rows[0])
	}
	if rows[0].Runs != 2 || rows[0].Failures != 1 {
		t.Errorf("linux-a runs=%d fails=%d", rows[0].Runs, rows[0].Failures)
	}
	if rows[0].TotalMs != 1000+5000+200+1000+200+100 {
		t.Errorf("linux-a total=%d", rows[0].TotalMs)
	}
}

func TestUsageGroupByProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")
	runsDir := filepath.Join(home, ".vmlab", "runs")
	writeRun(t, runsDir, "r1", "hetzner", "linux-a", 0, 1000, 5000, 200, 0)
	writeRun(t, runsDir, "r2", "hetzner", "linux-b", 0, 500, 100, 50, 0)
	writeRun(t, runsDir, "r3", "azure", "win-b", 0, 1500, 800, 300, 0)

	cmd := newUsageCmd()
	cmd.SetArgs([]string{"--json", "--group-by", "provider"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []usageRow
	_ = json.Unmarshal(out.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("want 2 grouped rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Instance != "" {
			t.Errorf("expected instance empty in provider grouping: %+v", r)
		}
	}
}

func TestUsageSinceFilter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")
	runsDir := filepath.Join(home, ".vmlab", "runs")
	writeRun(t, runsDir, "old", "hetzner", "linux-a", 0, 1000, 5000, 200, 48*time.Hour)
	writeRun(t, runsDir, "new", "hetzner", "linux-a", 0, 500, 100, 50, time.Minute)

	cmd := newUsageCmd()
	cmd.SetArgs([]string{"--json", "--since", "1h"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []usageRow
	_ = json.Unmarshal(out.Bytes(), &rows)
	if len(rows) != 1 || rows[0].Runs != 1 {
		t.Fatalf("--since 1h should only see the new run: %+v", rows)
	}
}

func TestUsageNoLifecycleEntriesIsBenign(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")
	cmd := newUsageCmd()
	cmd.SetArgs([]string{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "no runs with lifecycle data") {
		t.Fatalf("expected empty-state message; got %q", out.String())
	}
}
