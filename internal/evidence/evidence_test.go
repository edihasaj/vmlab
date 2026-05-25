package evidence

import (
	"io"
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

func TestStatusReflectsRunningThenFinished(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	if err := r.MarkRunning(); err != nil {
		t.Fatal(err)
	}
	st, err := Status(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running {
		t.Fatal("expected running=true after MarkRunning")
	}
	if st.ExitCode != nil {
		t.Errorf("running run should not have ExitCode yet")
	}
	if _, err := r.Finish(7); err != nil {
		t.Fatal(err)
	}
	st, err = Status(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Running {
		t.Fatal("expected running=false after Finish")
	}
	if st.ExitCode == nil || *st.ExitCode != 7 {
		t.Errorf("expected ExitCode=7, got %v", st.ExitCode)
	}
}

func TestLogCursorRoundTrip(t *testing.T) {
	c := LogCursor{"a:stdout": 123, "b:stderr": 456}
	out := ParseLogCursor(c.String())
	if out["a:stdout"] != 123 || out["b:stderr"] != 456 {
		t.Fatalf("round trip lost data: %v", out)
	}
}

func TestReadLogChunksTailsNewBytes(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	out, errW, logs, err := r.TargetWriters("alpha", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	_, _ = out.Write([]byte("hello "))
	_, _ = errW.Write([]byte("oops "))

	chunks, cursor, err := ReadLogChunks(r.Dir, nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (stdout+stderr), got %d", len(chunks))
	}
	// Write more, then resume from the cursor.
	_, _ = out.Write([]byte("world"))
	chunks2, _, err := ReadLogChunks(r.Dir, cursor, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks2) != 1 || chunks2[0].Stream != "stdout" || chunks2[0].Bytes != "world" {
		t.Fatalf("expected only the new stdout slice 'world', got %+v", chunks2)
	}
}

func TestPruneToFitSize(t *testing.T) {
	dir := t.TempDir()
	// Make three runs with descending StartedAt so the oldest is the first
	// to be trimmed. Each one carries a 1KB filler file so total size is
	// predictable enough to test the size cap.
	for i, age := range []time.Duration{72 * time.Hour, 48 * time.Hour, 24 * time.Hour} {
		r, _ := New(dir)
		_ = r.WriteFile("filler.bin", make([]byte, 4096))
		_, _ = r.Finish(0)
		path := filepath.Join(r.Dir, "meta.json")
		started := time.Now().Add(-age).UTC().Format(time.RFC3339)
		doctored := []byte(`{"id":"r` + string(rune('0'+i)) + `","startedAt":"` + started + `","targets":[]}`)
		if err := os.WriteFile(path, doctored, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Cap at ~6KB — should evict the oldest run (24KB ≫ 6KB).
	n, err := PruneToFitSize(dir, 6*1024)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("expected size prune to evict at least one run, got %d", n)
	}
}
