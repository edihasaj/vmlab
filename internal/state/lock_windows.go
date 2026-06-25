//go:build windows

package state

import (
	"os"

	"golang.org/x/sys/windows"
)

// errLockBusy is the sentinel Acquire compares against to tell "another process
// holds the lock" apart from a real error. LockFileEx with
// LOCKFILE_FAIL_IMMEDIATELY reports contention as ERROR_LOCK_VIOLATION.
var errLockBusy error = windows.ERROR_LOCK_VIOLATION

// lockWhole locks the entire file (offset 0, length 0xffffffffffffffff) — the
// Windows equivalent of a whole-file flock.
func lockWhole(f *os.File, flags uint32) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), ol)
}

func lockExclusiveNB(f *os.File) error {
	return lockWhole(f, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
}

func lockExclusive(f *os.File) error {
	return lockWhole(f, windows.LOCKFILE_EXCLUSIVE_LOCK)
}

func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), ol)
}
