//go:build !windows

package state

import (
	"os"
	"syscall"
)

// errLockBusy is the sentinel Acquire compares against to tell "another process
// holds the lock" apart from a real error. On unix flock(LOCK_NB) reports this
// as EWOULDBLOCK.
var errLockBusy error = syscall.EWOULDBLOCK

func lockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
