package evidence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunLifecycle(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.SetCmd("echo hi")
	r.SetSelector("all")

	out, errW, logs, err := r.TargetWriters("foo", os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	out.Write([]byte("stdout-line\n"))
	errW.Write([]byte("stderr-line\n"))
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := r.WriteSteps("foo", []map[string]any{{"index": 0, "ok": true}}); err != nil {
		t.Fatal(err)
	}
	r.AddTarget(TargetSummary{Name: "foo", Transport: "local", ExitCode: 0})
	meta, err := r.Finish(0)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ExitCode != 0 || len(meta.Targets) != 1 {
		t.Fatalf("bad meta: %+v", meta)
	}

	got, err := Read(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != meta.ID {
		t.Fatalf("read mismatch")
	}

	zip := filepath.Join(t.TempDir(), "out.zip")
	if err := Bundle(r.Dir, zip); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(zip); err != nil || st.Size() == 0 {
		t.Fatalf("bundle: %v size=%v", err, st)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	r.AddTarget(TargetSummary{Name: "x"})
	_, _ = r.Finish(0)
	// Backdate meta by rewriting StartedAt.
	meta, _ := Read(r.Dir)
	meta.StartedAt = time.Now().Add(-48 * time.Hour)
	bs, _ := os.ReadFile(filepath.Join(r.Dir, "meta.json"))
	_ = bs
	// Easiest: directly write a doctored meta.
	path := filepath.Join(r.Dir, "meta.json")
	doctored := []byte(`{"id":"old","startedAt":"2000-01-01T00:00:00Z","targets":[]}`)
	if err := os.WriteFile(path, doctored, 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := PruneOlderThan(dir, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
}
