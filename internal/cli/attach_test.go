package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/edihasaj/vmlab/internal/evidence"
)

// TestRunningLockRoundTrip exercises evidence.MarkRunning + ReadRunningState
// + Finish-removes-lock so attach has something to read while alive.
func TestRunningLockRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")

	run, err := evidence.New(filepath.Join(home, ".vmlab", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := run.MarkRunning(); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	st, err := evidence.ReadRunningState(run.Dir)
	if err != nil {
		t.Fatalf("ReadRunningState: %v", err)
	}
	if st.PID != os.Getpid() {
		t.Errorf("pid mismatch: %d vs %d", st.PID, os.Getpid())
	}
	if !runIsLive(run.Dir) {
		t.Error("runIsLive should be true after MarkRunning")
	}

	if _, err := run.Finish(0); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if runIsLive(run.Dir) {
		t.Error("running.lock should be gone after Finish")
	}
	if _, err := evidence.ReadRunningState(run.Dir); err == nil {
		t.Error("ReadRunningState should error after Finish")
	}
}

func TestLogTailReadsAppendedBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stdout.log")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	t1 := &logTail{path: path, label: "[a] ", out: &buf}
	if err := t1.drain(); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := buf.String(); got != "[a] hello\n" {
		t.Fatalf("first drain: %q", got)
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("world\n")
	_ = f.Close()

	if err := t1.drain(); err != nil {
		t.Fatalf("drain 2: %v", err)
	}
	if got := buf.String(); got != "[a] hello\n[a] world\n" {
		t.Fatalf("second drain: %q", got)
	}
}

func TestAttachExitsWhenLockRemoved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")

	run, err := evidence.New(filepath.Join(home, ".vmlab", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	_ = run.MarkRunning()
	// Create a target dir with a log file
	targetDir := filepath.Join(run.Dir, "targets", "smoke")
	_ = os.MkdirAll(targetDir, 0o755)
	logPath := filepath.Join(targetDir, "stdout.log")
	_ = os.WriteFile(logPath, []byte("kickoff\n"), 0o644)

	cmd := newAttachCmd()
	cmd.SetArgs([]string{filepath.Base(run.Dir)})
	var out bytes.Buffer
	cmd.SetOut(&out)

	// Remove the lock after a short delay so attach exits cleanly.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = run.Finish(0)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("attach: %v: %s", err, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("kickoff")) {
		t.Errorf("expected log line in output, got: %s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("run finished")) {
		t.Errorf("expected finish marker, got: %s", out.String())
	}
}
