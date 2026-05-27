package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withFakeCrabboxRunner swaps the runner for a capture so tests don't need a
// real crabbox binary on PATH. Returns a restore func.
func withFakeCrabboxRunner(t *testing.T, runErr error) *[]string {
	t.Helper()
	prior := crabboxRunner
	captured := []string{}
	crabboxRunner = func(_ *cobra.Command, args []string) error {
		captured = append([]string(nil), args...)
		return runErr
	}
	t.Cleanup(func() { crabboxRunner = prior })
	return &captured
}

func TestCrabboxCmdHasExpectedSubcommands(t *testing.T) {
	c := newCrabboxCmd()
	want := map[string]bool{"checkpoint": true, "warmup": true, "image": true, "pool": true}
	got := map[string]bool{}
	for _, sub := range c.Commands() {
		got[sub.Name()] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("expected `crabbox %s` subcommand, missing", k)
		}
	}
}

func TestCrabboxCheckpointForwardsArgsVerbatim(t *testing.T) {
	captured := withFakeCrabboxRunner(t, nil)
	root := newCrabboxCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetContext(context.Background())
	root.SetArgs([]string{"checkpoint", "create", "--id", "blue-lobster", "--name", "after-npm-ci"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []string{"checkpoint", "create", "--id", "blue-lobster", "--name", "after-npm-ci"}
	if len(*captured) != len(want) {
		t.Fatalf("arg count: got %v want %v", *captured, want)
	}
	for i, a := range want {
		if (*captured)[i] != a {
			t.Errorf("arg[%d]: got %q want %q", i, (*captured)[i], a)
		}
	}
}

func TestCrabboxWarmupForwardsParallelsProvider(t *testing.T) {
	captured := withFakeCrabboxRunner(t, nil)
	root := newCrabboxCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetContext(context.Background())
	root.SetArgs([]string{"warmup", "--provider", "parallels", "--class", "macos"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*captured) < 1 || (*captured)[0] != "warmup" {
		t.Fatalf("first arg should be subcommand: %v", *captured)
	}
	if !contains(*captured, "parallels") {
		t.Errorf("expected `parallels` flag value to pass through: %v", *captured)
	}
}

func TestCrabboxPropagatesExitError(t *testing.T) {
	withFakeCrabboxRunner(t, errFake{"crabbox exited 3"})
	root := newCrabboxCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetContext(context.Background())
	root.SetArgs([]string{"checkpoint", "fork", "chk_nope"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "crabbox exited 3") {
		t.Fatalf("expected exit error to propagate, got %v", err)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

type errFake struct{ msg string }

func (e errFake) Error() string { return e.msg }
