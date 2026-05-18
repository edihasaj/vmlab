package hooks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// fakeTransport records every Run() invocation so tests can assert what
// target-side hooks dispatched, without touching a real guest.
type fakeTransport struct {
	calls    [][]string
	wantFail bool
}

func (f *fakeTransport) Name() string                      { return "fake" }
func (f *fakeTransport) Capabilities() transport.Caps      { return transport.Caps{} }
func (f *fakeTransport) Doctor(context.Context, target.Target) transport.Health {
	return transport.Health{}
}
func (f *fakeTransport) Sync(context.Context, target.Target, string) error { return nil }
func (f *fakeTransport) Run(_ context.Context, _ target.Target, cmd []string, stdout, stderr io.Writer) (transport.Result, error) {
	f.calls = append(f.calls, append([]string(nil), cmd...))
	if stdout != nil {
		_, _ = stdout.Write([]byte("ok\n"))
	}
	if f.wantFail {
		return transport.Result{ExitCode: 1}, errors.New("guest failure")
	}
	return transport.Result{ExitCode: 0}, nil
}
func (f *fakeTransport) Shell(context.Context, target.Target) error            { return nil }
func (f *fakeTransport) Screenshot(context.Context, target.Target, string) error { return nil }
func (f *fakeTransport) GUI(context.Context, target.Target, transport.GUIAction) error {
	return nil
}

func TestRunHostShellStep(t *testing.T) {
	var out bytes.Buffer
	r := &Runner{Stdout: &out, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePreUp, []Step{{Run: "printf hello"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Fatalf("expected host shell output, got %q", out.String())
	}
}

func TestRunHostExecStep(t *testing.T) {
	var out bytes.Buffer
	r := &Runner{Stdout: &out, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePreUp, []Step{{Exec: []string{"printf", "argv"}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "argv") {
		t.Fatalf("expected exec output, got %q", out.String())
	}
}

func TestRunTargetStepRoutesToTransport(t *testing.T) {
	tr := &fakeTransport{}
	r := &Runner{Transport: tr, Stdout: io.Discard, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePostUp, []Step{
		{Target: "uname -a"},
		{TargetExec: []string{"echo", "ready"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(tr.calls) != 2 {
		t.Fatalf("want 2 transport calls, got %d", len(tr.calls))
	}
	if tr.calls[0][0] != "sh" || tr.calls[0][1] != "-lc" {
		t.Fatalf("first call not sh -lc: %v", tr.calls[0])
	}
	if tr.calls[1][0] != "echo" {
		t.Fatalf("second call lost argv: %v", tr.calls[1])
	}
}

func TestStopsOnFirstFailure(t *testing.T) {
	tr := &fakeTransport{wantFail: true}
	r := &Runner{Transport: tr, Stdout: io.Discard, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePostUp, []Step{
		{Target: "should-fail"},
		{Target: "never-runs"},
	})
	if err == nil {
		t.Fatalf("expected failure")
	}
	if len(tr.calls) != 1 {
		t.Fatalf("expected to stop after first failure, got %d calls", len(tr.calls))
	}
}

func TestIgnoreFailureContinues(t *testing.T) {
	tr := &fakeTransport{wantFail: true}
	r := &Runner{Transport: tr, Stdout: io.Discard, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePostUp, []Step{
		{Target: "first", IgnoreFail: true},
		{Target: "second", IgnoreFail: true},
	})
	if err != nil {
		t.Fatalf("ignore_failure should swallow: %v", err)
	}
	if len(tr.calls) != 2 {
		t.Fatalf("expected both to run, got %d", len(tr.calls))
	}
}

func TestTargetHookWithoutTransportFails(t *testing.T) {
	r := &Runner{Stdout: io.Discard, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePostUp, []Step{{Target: "uname"}})
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestEnvResolverPlainPasses(t *testing.T) {
	var out bytes.Buffer
	r := &Runner{Stdout: &out, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePreUp, []Step{
		{Run: "printf %s \"$MY_TOKEN\"", Env: map[string]string{"MY_TOKEN": "literal-secret"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "literal-secret") {
		t.Fatalf("env not injected: %q", out.String())
	}
}

func TestEmptyStepRejected(t *testing.T) {
	r := &Runner{Stdout: io.Discard, Stderr: io.Discard}
	err := r.Run(context.Background(), PhasePreUp, []Step{{}})
	if err == nil {
		t.Fatalf("expected error for empty step")
	}
}

func TestConfigEmpty(t *testing.T) {
	c := Config{}
	if !c.Empty() {
		t.Fatal("empty config should be Empty()")
	}
	c.PreUp = []Step{{Run: "x"}}
	if c.Empty() {
		t.Fatal("non-empty config reported Empty()")
	}
}
