package fleet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// stubTransport is a no-op transport used in tests.
type stubTransport struct{ name string }

func (s *stubTransport) Name() string                 { return s.name }
func (s *stubTransport) Capabilities() transport.Caps { return transport.Caps{Shell: true} }
func (s *stubTransport) Doctor(ctx context.Context, t target.Target) transport.Health {
	return transport.Health{OK: true}
}
func (s *stubTransport) Sync(ctx context.Context, t target.Target, src string) error     { return nil }
func (s *stubTransport) Shell(ctx context.Context, t target.Target) error                { return nil }
func (s *stubTransport) Screenshot(ctx context.Context, t target.Target, p string) error { return nil }
func (s *stubTransport) GUI(ctx context.Context, t target.Target, a transport.GUIAction) error {
	return nil
}
func (s *stubTransport) Run(ctx context.Context, t target.Target, cmd []string, so, se io.Writer) (transport.Result, error) {
	return transport.Result{}, nil
}

func TestRunPrefixesAndAggregates(t *testing.T) {
	reg := transport.NewRegistry()
	reg.Register(&stubTransport{name: "stub"})
	targets := []target.Target{
		{Name: "a", Transport: "stub"},
		{Name: "b", Transport: "stub"},
	}
	var stdout, stderr bytes.Buffer
	var counter atomic.Int32
	outs, err := Run(context.Background(), targets, reg, Options{}, &stdout, &stderr,
		func(ctx context.Context, tgt target.Target, tr transport.Transport, so, se io.Writer) (int, error) {
			counter.Add(1)
			io.WriteString(so, "hello "+tgt.Name+"\n")
			return 0, nil
		})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if counter.Load() != 2 {
		t.Fatalf("job ran %d times, want 2", counter.Load())
	}
	if !strings.Contains(stdout.String(), "[a] hello a") || !strings.Contains(stdout.String(), "[b] hello b") {
		t.Fatalf("missing prefixes:\n%s", stdout.String())
	}
	if AggregateExit(outs) != 0 {
		t.Fatalf("expected exit 0")
	}
}

func TestFailFastStopsLaunching(t *testing.T) {
	reg := transport.NewRegistry()
	reg.Register(&stubTransport{name: "stub"})
	targets := make([]target.Target, 5)
	for i := range targets {
		targets[i] = target.Target{Name: string(rune('a' + i)), Transport: "stub"}
	}
	var ran atomic.Int32
	_, err := Run(context.Background(), targets, reg, Options{MaxParallel: 1, FailFast: true}, io.Discard, io.Discard,
		func(ctx context.Context, tgt target.Target, tr transport.Transport, so, se io.Writer) (int, error) {
			ran.Add(1)
			if tgt.Name == "a" {
				return 1, errors.New("boom")
			}
			time.Sleep(10 * time.Millisecond)
			return 0, nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if ran.Load() == int32(len(targets)) {
		t.Fatalf("fail-fast didn't stop subsequent launches; ran=%d", ran.Load())
	}
}

func TestContinueOnError(t *testing.T) {
	reg := transport.NewRegistry()
	reg.Register(&stubTransport{name: "stub"})
	targets := []target.Target{
		{Name: "a", Transport: "stub"},
		{Name: "b", Transport: "stub"},
	}
	var ran atomic.Int32
	outs, err := Run(context.Background(), targets, reg, Options{ContinueOnError: true}, io.Discard, io.Discard,
		func(ctx context.Context, tgt target.Target, tr transport.Transport, so, se io.Writer) (int, error) {
			ran.Add(1)
			if tgt.Name == "a" {
				return 1, errors.New("a failed")
			}
			return 0, nil
		})
	if err == nil {
		t.Fatal("expected error after run because exit !=0")
	}
	if ran.Load() != 2 {
		t.Fatalf("continue-on-error should run all: got %d", ran.Load())
	}
	if AggregateExit(outs) == 0 {
		t.Fatalf("expected non-zero aggregate exit")
	}
}
