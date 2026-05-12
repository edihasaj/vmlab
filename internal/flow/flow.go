// Package flow loads and runs vmlab YAML flows.
//
// A flow is intentionally minimal — `run` and `assert` shell steps. Anything
// more complex belongs in a script the flow invokes.
package flow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/schema"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"gopkg.in/yaml.v3"
)

// Step is one shell action. Exactly one of Run, Assert, or Exec must be set.
//
//   - run    — shell line, wrapped via `sh -lc` (POSIX).
//   - assert — same wrapping but failures stop the flow with a clear message.
//   - exec   — argv passed directly to the transport, no shell wrapping.
//     Use this for Windows guests (e.g. `["cmd.exe", "/c", "ver"]`) or
//     anywhere `sh` is not available.
type Step struct {
	Run    string   `yaml:"run,omitempty"`
	Assert string   `yaml:"assert,omitempty"`
	Exec   []string `yaml:"exec,omitempty"`
	Name   string   `yaml:"name,omitempty"`
}

// Flow is a sequence of steps.
type Flow struct {
	Name  string `yaml:"name,omitempty"`
	Steps []Step `yaml:"steps"`

	// SourceFile records where the flow was loaded from.
	SourceFile string `yaml:"-"`
}

// Load parses a flow file.
func Load(path string) (*Flow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read flow %s: %w", path, err)
	}
	if err := schema.ValidateFlow(path, data); err != nil {
		return nil, err
	}
	var f Flow
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse flow %s: %w", path, err)
	}
	if len(f.Steps) == 0 {
		return nil, fmt.Errorf("flow %s has no steps", path)
	}
	if f.Name == "" {
		f.Name = strings.TrimSuffix(stripDir(path), extOf(path))
	}
	f.SourceFile = path
	return &f, nil
}

// LooksLikeFlowPath reports whether arg is a YAML file path (heuristic).
func LooksLikeFlowPath(s string) bool {
	if !strings.HasSuffix(s, ".yaml") && !strings.HasSuffix(s, ".yml") {
		return false
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

// StepResult captures one step's outcome.
type StepResult struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	Cmd        string `json:"cmd"`
	Name       string `json:"name,omitempty"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// Run executes a flow against a target via tr, streaming output to stdout/stderr.
// failFast: if true (default for asserts), stop after the first failure.
func Run(ctx context.Context, tr transport.Transport, t target.Target, f *Flow, stdout, stderr io.Writer) ([]StepResult, error) {
	results := make([]StepResult, 0, len(f.Steps))
	for i, s := range f.Steps {
		var kind, cmd string
		var argv []string
		switch {
		case len(s.Exec) > 0:
			kind, argv = "exec", s.Exec
			cmd = strings.Join(argv, " ")
		case s.Run != "":
			kind, cmd = "run", s.Run
			argv = []string{"sh", "-lc", cmd}
		case s.Assert != "":
			kind, cmd = "assert", s.Assert
			argv = []string{"sh", "-lc", cmd}
		default:
			return results, fmt.Errorf("step %d: must set run, assert, or exec", i)
		}
		start := time.Now()
		slog.Debug("flow step", "target", t.Name, "index", i, "kind", kind, "cmd", oneLine(cmd))
		fmt.Fprintf(stderr, "step %d (%s): %s\n", i, kind, oneLine(cmd))
		res, err := tr.Run(ctx, t, argv, stdout, stderr)
		dur := time.Since(start)
		sr := StepResult{Index: i, Kind: kind, Cmd: cmd, Name: s.Name, ExitCode: res.ExitCode, DurationMs: dur.Milliseconds()}
		if err != nil {
			sr.Error = err.Error()
			results = append(results, sr)
			return results, err
		}
		results = append(results, sr)
		if res.ExitCode != 0 {
			return results, fmt.Errorf("step %d (%s) exited %d", i, kind, res.ExitCode)
		}
	}
	return results, nil
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

func stripDir(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func extOf(p string) string {
	if i := strings.LastIndex(p, "."); i > 0 {
		return p[i:]
	}
	return ""
}
