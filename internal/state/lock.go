// Package state owns ephemeral, on-disk bookkeeping under ~/.vmlab/state/.
// Today: file locks that serialize lifecycle ops on a single instance so two
// terminals don't race Up/Down on the same VM.
package state

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Lock is a held flock on an instance's state file.
type Lock struct {
	f    *os.File
	path string
}

// Acquire takes an exclusive lock on <stateDir>/<instance>.lock. If another
// process already holds the lock, it prints a one-line notice to notify and
// then waits (no timeout — interruptible via ctrl-C).
//
// notify is called once if and only if we have to wait. Pass nil to suppress.
func Acquire(stateDir, instance string, notify func(holderPID string)) (*Lock, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	path := filepath.Join(stateDir, sanitize(instance)+".lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	// Try non-blocking first so we can emit a "waiting for X" notice before
	// blocking forever.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("flock %s: %w", path, err)
		}
		if notify != nil {
			notify(readHolder(f))
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, fmt.Errorf("flock %s: %w", path, err)
		}
	}
	// We now hold the lock. Record our PID + timestamp so a future waiter
	// can name the holder.
	_ = f.Truncate(0)
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		fmt.Fprintf(f, "pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	}
	return &Lock{f: f, path: path}, nil
}

// Release unlocks and closes the file. Idempotent.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}

func readHolder(f *os.File) string {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "unknown"
	}
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	if n == 0 {
		return "unknown"
	}
	for _, line := range strings.Split(strings.TrimSpace(string(buf[:n])), "\n") {
		if strings.HasPrefix(line, "pid=") {
			pid := strings.TrimPrefix(line, "pid=")
			if _, err := strconv.Atoi(pid); err == nil {
				return pid
			}
		}
	}
	return "unknown"
}

var pathRepl = strings.NewReplacer("/", "_", "\\", "_", " ", "_")

func sanitize(s string) string { return pathRepl.Replace(s) }
