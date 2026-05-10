package evidence

import (
	"os"
	"strings"
	"testing"
)

func TestWriteJUnit(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	steps := []Step{
		{Index: 0, Kind: "run", Cmd: "echo hi", ExitCode: 0, DurationMs: 5},
		{Index: 1, Kind: "assert", Cmd: "false", ExitCode: 1, DurationMs: 2, Error: "step 1 (assert) exited 1"},
	}
	if _, err := r.WriteSteps("ubuntu", steps); err != nil {
		t.Fatal(err)
	}
	r.AddTarget(TargetSummary{Name: "ubuntu", Transport: "crabbox", ExitCode: 1, Error: "boom", Duration: 12})
	if _, err := r.Finish(1); err != nil {
		t.Fatal(err)
	}
	path, err := r.WriteJUnit()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"testsuites", "ubuntu", "step-1-assert", "<failure"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in junit.xml:\n%s", want, got)
		}
	}
}

func TestWriteJUnitNoSteps(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	r.AddTarget(TargetSummary{Name: "x", Transport: "local", ExitCode: 0, Duration: 1})
	_, _ = r.Finish(0)
	path, err := r.WriteJUnit()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "command") {
		t.Fatalf("expected fallback command case:\n%s", data)
	}
}
