// Package fleet runs operations across multiple targets concurrently.
package fleet

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// Options control fan-out execution.
type Options struct {
	MaxParallel     int  // 0 = unlimited
	FailFast        bool // stop launching new work after first failure
	ContinueOnError bool // ignore failures and run all targets
}

// TargetOutcome is one target's slice of a fan-out run.
type TargetOutcome struct {
	Target    target.Target
	ExitCode  int
	Error     error
	StartedAt time.Time
	Duration  time.Duration
}

// Failed reports whether the outcome is a failure.
func (o TargetOutcome) Failed() bool { return o.Error != nil || o.ExitCode != 0 }

// Job is the per-target work fn. It must respect ctx for cancellation.
type Job func(ctx context.Context, t target.Target, tr transport.Transport, stdout, stderr io.Writer) (exitCode int, err error)

// Run executes job across targets according to opts. Output is wrapped with
// per-target prefixes so multi-target streams stay readable.
func Run(ctx context.Context, targets []target.Target, registry *transport.Registry, opts Options, stdout, stderr io.Writer, job Job) ([]TargetOutcome, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no targets")
	}
	max := opts.MaxParallel
	if max <= 0 || max > len(targets) {
		max = len(targets)
	}

	sem := make(chan struct{}, max)
	outcomes := make([]TargetOutcome, len(targets))

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Single mutex shared by every prefixedWriter so writes to the underlying
	// stdout/stderr from concurrent goroutines stay serialised.
	var writeMu sync.Mutex

	var failed atomic.Bool
	var wg sync.WaitGroup
	for i, t := range targets {
		i, t := i, t
		wg.Add(1)
		select {
		case <-runCtx.Done():
			outcomes[i] = TargetOutcome{Target: t, Error: runCtx.Err()}
			wg.Done()
			continue
		default:
		}
		if opts.FailFast && failed.Load() {
			outcomes[i] = TargetOutcome{Target: t, Error: fmt.Errorf("skipped (fail-fast)")}
			wg.Done()
			continue
		}
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			tr, err := registry.Get(t.Transport)
			if err != nil {
				outcomes[i] = TargetOutcome{Target: t, Error: err, StartedAt: time.Now()}
				if !opts.ContinueOnError {
					failed.Store(true)
					if opts.FailFast {
						cancel()
					}
				}
				return
			}
			pw := newPrefixedWriter(stdout, t.Name, &writeMu)
			pe := newPrefixedWriter(stderr, t.Name, &writeMu)
			start := time.Now()
			exit, err := job(runCtx, t, tr, pw, pe)
			pw.Flush()
			pe.Flush()
			outcomes[i] = TargetOutcome{
				Target:    t,
				ExitCode:  exit,
				Error:     err,
				StartedAt: start,
				Duration:  time.Since(start),
			}
			if (err != nil || exit != 0) && !opts.ContinueOnError {
				failed.Store(true)
				if opts.FailFast {
					cancel()
				}
			}
		}()
	}
	wg.Wait()

	if anyFailed(outcomes) {
		return outcomes, fmt.Errorf("one or more targets failed")
	}
	return outcomes, nil
}

func anyFailed(outs []TargetOutcome) bool {
	for _, o := range outs {
		if o.Failed() {
			return true
		}
	}
	return false
}

// AggregateExit returns the highest non-zero exit code across outcomes,
// or 1 if any error is present without an exit code.
func AggregateExit(outs []TargetOutcome) int {
	max := 0
	for _, o := range outs {
		if o.ExitCode > max {
			max = o.ExitCode
		}
		if o.Error != nil && max == 0 {
			max = 1
		}
	}
	return max
}

// prefixedWriter wraps lines with a [name] prefix and serialises every write to
// the underlying destination through a Run-wide mutex so concurrent targets can
// share one stdout/stderr without corruption.
type prefixedWriter struct {
	mu      sync.Mutex  // protects pending; line-buffering is per-target
	writeMu *sync.Mutex // shared across all prefixedWriters for one Run
	dst     io.Writer
	prefix  string
	pending []byte
}

func newPrefixedWriter(dst io.Writer, name string, writeMu *sync.Mutex) *prefixedWriter {
	if dst == nil {
		dst = io.Discard
	}
	return &prefixedWriter{
		dst:     dst,
		prefix:  "[" + name + "] ",
		writeMu: writeMu,
	}
}

func (w *prefixedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		i := indexNL(w.pending)
		if i < 0 {
			break
		}
		line := append([]byte(w.prefix), w.pending[:i+1]...)
		w.pending = w.pending[i+1:]
		if err := w.emit(line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes any pending unterminated bytes.
func (w *prefixedWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		line := append([]byte(w.prefix), w.pending...)
		line = append(line, '\n')
		w.pending = nil
		_ = w.emit(line)
	}
}

func (w *prefixedWriter) emit(line []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	_, err := w.dst.Write(line)
	return err
}

func indexNL(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}
