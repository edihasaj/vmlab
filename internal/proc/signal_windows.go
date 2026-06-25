//go:build windows

package proc

import "os"

// sendSignal terminates the process. Windows has no POSIX signals and no
// reliable way to deliver a Ctrl-C to an unrelated process, so INT/TERM/KILL
// all force-terminate via TerminateProcess (os.Process.Kill). Runs whose
// cleanup relies on trapping SIGINT will not get to run those hooks on Windows.
func sendSignal(pid int, _ Sig) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
