//go:build !windows

package proc

import "syscall"

func sendSignal(pid int, s Sig) error {
	var sig syscall.Signal
	switch s {
	case SigTerm:
		sig = syscall.SIGTERM
	case SigKill:
		sig = syscall.SIGKILL
	default:
		sig = syscall.SIGINT
	}
	return syscall.Kill(pid, sig)
}
