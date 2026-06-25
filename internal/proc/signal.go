// Package proc sends signals to other processes by PID in a cross-platform way.
// vmlab uses it to cancel a running run (attach --signal, MCP vmlab_cancel).
//
// On unix the three signals map to SIGINT / SIGTERM / SIGKILL, so a run's
// cleanup hooks fire on INT/TERM. Windows has no POSIX signals: all three
// terminate the process (see signal_windows.go), so cleanup hooks that rely on
// catching SIGINT do not run there — a documented platform limitation.
package proc

import (
	"fmt"
	"strings"
)

// Sig is a platform-neutral signal kind.
type Sig int

const (
	SigInt  Sig = iota // graceful interrupt (SIGINT on unix)
	SigTerm            // terminate (SIGTERM on unix)
	SigKill            // force kill (SIGKILL on unix)
)

func (s Sig) String() string {
	switch s {
	case SigInt:
		return "INT"
	case SigTerm:
		return "TERM"
	case SigKill:
		return "KILL"
	default:
		return "INT"
	}
}

// Parse maps a user-supplied name to a Sig. Accepts "", INT/SIGINT,
// TERM/SIGTERM, KILL/SIGKILL (case-insensitive). Empty defaults to INT.
func Parse(name string) (Sig, error) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "", "INT", "SIGINT":
		return SigInt, nil
	case "TERM", "SIGTERM":
		return SigTerm, nil
	case "KILL", "SIGKILL":
		return SigKill, nil
	default:
		return SigInt, fmt.Errorf("unknown signal %q (use INT|TERM|KILL)", name)
	}
}

// Send delivers s to the process identified by pid. Platform-specific
// implementation lives in signal_unix.go / signal_windows.go.
func Send(pid int, s Sig) error { return sendSignal(pid, s) }
