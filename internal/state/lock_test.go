package state

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir, "win11", nil)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Re-acquire works after release.
	l2, err := Acquire(dir, "win11", nil)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireBlocksWhenHeld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock semantics differ on Windows")
	}
	dir := t.TempDir()
	first, err := Acquire(dir, "win11", nil)
	if err != nil {
		t.Fatal(err)
	}

	var notified atomic.Bool
	done := make(chan struct{})
	go func() {
		l, err := Acquire(dir, "win11", func(pid string) { notified.Store(true) })
		if err != nil {
			t.Errorf("contended acquire: %v", err)
			close(done)
			return
		}
		_ = l.Release()
		close(done)
	}()

	// Give the goroutine time to attempt + emit the wait notice.
	time.Sleep(150 * time.Millisecond)
	if !notified.Load() {
		t.Errorf("expected wait notice while lock was held")
	}
	select {
	case <-done:
		t.Fatal("contended acquire returned before first lock released")
	default:
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("contended acquire never unblocked")
	}
}

func TestSanitizeReplaces(t *testing.T) {
	cases := map[string]string{
		"win11":         "win11",
		"win 11":        "win_11",
		"team/instance": "team_instance",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestNilReleaseSafe(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("nil release: %v", err)
	}
}
