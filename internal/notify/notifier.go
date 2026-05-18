// Package notify posts vmlab lifecycle events to external channels
// (Discord today; Slack/Telegram via the same interface tomorrow).
//
// Wired into `vmlab with` and `vmlab run @<inst>` only — these own the full
// up→run→down lifecycle and have the data a notifier wants.
package notify

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Phase enumerates the lifecycle moments a notifier may fire on.
type Phase int

const (
	PhaseStart Phase = iota
	PhaseSuccess
	PhaseFailure
)

// String returns the canonical lower-case name used in config (`on: [start,
// success, failure]`).
func (p Phase) String() string {
	switch p {
	case PhaseStart:
		return "start"
	case PhaseSuccess:
		return "success"
	case PhaseFailure:
		return "failure"
	}
	return "unknown"
}

// ParsePhase is the inverse of Phase.String.
func ParsePhase(s string) (Phase, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "start":
		return PhaseStart, true
	case "success":
		return PhaseSuccess, true
	case "failure", "fail", "error":
		return PhaseFailure, true
	}
	return 0, false
}

// Event is the payload passed to every Notifier. All fields are optional; the
// notifier renders what it has.
type Event struct {
	Phase       Phase
	Instance    string
	Provider    string
	RunID       string
	Selector    string
	Cmd         string
	UpMs        int64
	RunMs       int64
	DownMs      int64
	ExitCode    int
	Err         string
	StderrTail  string
	EvidenceDir string
}

// TotalMs returns the wall-clock duration covered by the lifecycle so far.
func (e Event) TotalMs() int64 { return e.UpMs + e.RunMs + e.DownMs }

// Notifier dispatches a single Event to one channel. Implementations should
// honour ctx for cancellation and avoid blocking longer than a few seconds.
type Notifier interface {
	Notify(ctx context.Context, ev Event) error
	Name() string
	Phases() map[Phase]bool
}

// Multi fans out one Event to many notifiers concurrently. Failures from
// individual notifiers are written to stderr but never bubbled up — a flaky
// Discord webhook should not fail a passing run.
type Multi struct {
	Notifiers []Notifier
	// Stderr receives one line per dispatch failure. nil = os.Stderr.
	Stderr interface {
		Write(p []byte) (int, error)
	}
}

// Notify dispatches ev to every notifier whose Phases() includes ev.Phase.
func (m *Multi) Notify(ctx context.Context, ev Event) {
	if m == nil || len(m.Notifiers) == 0 {
		return
	}
	if disabled() {
		return
	}
	var wg sync.WaitGroup
	for _, n := range m.Notifiers {
		if !n.Phases()[ev.Phase] {
			continue
		}
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.Notify(ctx, ev); err != nil {
				fmt.Fprintf(m.stderr(), "notify(%s): %v\n", n.Name(), err)
			}
		}()
	}
	wg.Wait()
}

func (m *Multi) stderr() interface {
	Write(p []byte) (int, error)
} {
	if m.Stderr != nil {
		return m.Stderr
	}
	return os.Stderr
}

// disabled returns true when the VMLAB_NOTIFY env var is set to a falsey value.
// Used to silence all notifiers for one-off runs without touching config.
func disabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("VMLAB_NOTIFY")))
	switch v {
	case "0", "false", "no", "off":
		return true
	}
	return false
}
