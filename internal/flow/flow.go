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

// Step is one shell action. Exactly one of Run, Assert, Exec, or Install
// must be set.
//
//   - run     — shell line, wrapped via `sh -lc` (POSIX).
//   - assert  — same wrapping but failures stop the flow with a clear message.
//   - exec    — argv passed directly to the transport, no shell wrapping.
//     Use this for Windows guests (e.g. `["cmd.exe", "/c", "ver"]`) or
//     anywhere `sh` is not available.
//   - install — per-OS map of shell lines. The flow runner picks the
//     line that matches the target's OSKind and treats it as `run`.
//     Lets one flow describe "install jq" without forking per OS:
//
//         - install:
//             mac:     brew install jq
//             linux:   sudo apt-get install -y jq
//             windows: choco install -y jq
//
// `when:` is an optional filter that gates whether the step runs against
// this target. Conditions are AND-joined comma-separated key=value or
// key!=value clauses. Recognised keys: `os`, `arch`. Examples:
//
//	when: os=linux
//	when: os!=darwin
//	when: os=linux,arch=arm64
type Step struct {
	Run      string            `yaml:"run,omitempty"`
	Assert   string            `yaml:"assert,omitempty"`
	Exec     []string          `yaml:"exec,omitempty"`
	Install  map[string]string `yaml:"install,omitempty"`
	Artifact *ArtifactSpec     `yaml:"artifact,omitempty"`
	Sync     string            `yaml:"sync,omitempty"`
	When     string            `yaml:"when,omitempty"`
	Name     string            `yaml:"name,omitempty"`
	// Workdir is the cwd inside the guest for run/assert. cmd.exe gets
	// pushd (works on UNC), sh gets cd. Step-level overrides flow-level.
	// Variables ($VMLAB_SYNC_DIR etc.) are substituted before use.
	Workdir string `yaml:"workdir,omitempty"`
	// Env exports K=V into the step's environment. Substitution applies
	// to values. Implemented as a shell-prefix so the transport contract
	// (which has no env channel) stays unchanged.
	Env map[string]string `yaml:"env,omitempty"`
}

// ArtifactSpec describes a host-side build that produces a binary per OS.
// vmlab caches the output by (src content + build cmd + os/arch) so a one-
// line source change only rebuilds the OS that was actually touched.
//
//   - src        — file or directory whose recursive content hash gates
//     the cache. Hidden files (".git" etc.) skipped, matching the watch
//     loop.
//   - build      — map of os-kind → shell command. The picked command
//     runs on the *host* (not the target), so a Mac host can
//     cross-compile a Linux/Windows binary without booting the VM.
//   - output     — host path the build is expected to drop a binary at,
//     keyed by os-kind. Required if delivery is on.
//   - deliver_to — path inside the target to deliver the picked
//     output[osKind] to. Empty = build-only (the user wires delivery
//     separately, e.g. via a follow-on `run:` with scp). When set,
//     vmlab calls the target's transport Sync after a successful build
//     (or cache hit) so the binary lands in the VM in one step.
type ArtifactSpec struct {
	Src       string            `yaml:"src,omitempty"`
	Build     map[string]string `yaml:"build,omitempty"`
	Output    map[string]string `yaml:"output,omitempty"`
	DeliverTo string            `yaml:"deliver_to,omitempty"`
}

// Flow is a sequence of steps.
type Flow struct {
	Name  string `yaml:"name,omitempty"`
	Steps []Step `yaml:"steps"`

	// Workdir, Env are flow-level defaults applied to every step unless the
	// step overrides them. Lets a flow say "everything runs in $VMLAB_SYNC_DIR"
	// once instead of repeating it.
	Workdir string            `yaml:"workdir,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`

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
//
// Steps gated by `when:` against a non-matching OS / arch are recorded as
// `skipped` and do not run — keeps matrix output honest without exploding
// the YAML into per-OS forks.
func Run(ctx context.Context, tr transport.Transport, t target.Target, f *Flow, stdout, stderr io.Writer) ([]StepResult, error) {
	results := make([]StepResult, 0, len(f.Steps))
	osKind := t.OSKind()
	arch := t.SettingString("arch") // optional; "" matches any arch clause
	rt := newRuntime(t)
	for i, s := range f.Steps {
		// Optional gate. A "no match" outcome is a deliberate skip, not a
		// failure — flows should describe the universe of targets and rely
		// on `when:` to pick the right rows.
		if s.When != "" {
			match, err := matchWhen(s.When, osKind, arch)
			if err != nil {
				return results, fmt.Errorf("step %d: when: %w", i, err)
			}
			if !match {
				results = append(results, StepResult{Index: i, Kind: "skip", Cmd: s.When, Name: s.Name})
				fmt.Fprintf(stderr, "step %d (skip): when=%q (os=%s arch=%s)\n", i, s.When, osKind, arch)
				continue
			}
		}

		// Artifact steps run on the *host*, not the target — they produce
		// the binary the target is about to install/run. Caching means a
		// repeat tick with the same source content is a microsecond stat.
		if s.Artifact != nil {
			start := time.Now()
			cmdLine, cached, err := runArtifactStep(ctx, s.Artifact, osKind, arch, artifactCacheDir(), tr, t, stdout, stderr)
			dur := time.Since(start)
			if err != nil {
				results = append(results, StepResult{Index: i, Kind: "artifact", Cmd: cmdLine, Name: s.Name, ExitCode: 1, DurationMs: dur.Milliseconds(), Error: err.Error()})
				return results, err
			}
			if cmdLine == "" {
				// No entry for this OS — treat as a skip, mirroring install:.
				results = append(results, StepResult{Index: i, Kind: "skip", Cmd: "artifact: no build entry for " + osKind, Name: s.Name})
				continue
			}
			label := "artifact"
			if cached {
				label = "artifact-cached"
			}
			fmt.Fprintf(stderr, "step %d (%s): %s\n", i, label, oneLine(cmdLine))
			results = append(results, StepResult{Index: i, Kind: label, Cmd: cmdLine, Name: s.Name, ExitCode: 0, DurationMs: dur.Milliseconds()})
			continue
		}

		// Sync step pushes a host path into the guest via the target's
		// transport. Lets a flow start with `- sync: .` so subsequent
		// `run:` steps can compile / test the just-shipped source tree.
		// After a successful Sync the transport's GuestMount (if it has
		// one) tells us where the bits landed inside the guest; the
		// path is exposed as $VMLAB_SYNC_DIR for later steps.
		if s.Sync != "" {
			start := time.Now()
			src := rt.substitute(s.Sync)
			fmt.Fprintf(stderr, "step %d (sync): %s\n", i, src)
			err := tr.Sync(ctx, t, src)
			dur := time.Since(start)
			sr := StepResult{Index: i, Kind: "sync", Cmd: src, Name: s.Name, DurationMs: dur.Milliseconds()}
			if err != nil {
				sr.ExitCode = 1
				sr.Error = err.Error()
				results = append(results, sr)
				return results, err
			}
			if mount := transport.GuestMountFor(tr, t, src); mount != "" {
				rt.set("VMLAB_SYNC_DIR", mount)
				fmt.Fprintf(stderr, "step %d (sync): VMLAB_SYNC_DIR=%s\n", i, mount)
			}
			results = append(results, sr)
			continue
		}

		// Effective workdir/env: step wins, else flow-level default.
		workdir := rt.substitute(s.Workdir)
		if workdir == "" {
			workdir = rt.substitute(f.Workdir)
		}
		env := mergedEnv(f.Env, s.Env, rt)

		var kind, cmd string
		var argv []string
		switch {
		case len(s.Install) > 0:
			pick, ok := pickInstall(s.Install, osKind)
			if !ok {
				// No entry for this OS → treat as a skip, identical to a
				// `when:` miss. Lets users describe a Linux-only install
				// without forcing them to add `when:` everywhere too.
				results = append(results, StepResult{Index: i, Kind: "skip", Cmd: "install: no entry for " + osKind, Name: s.Name})
				fmt.Fprintf(stderr, "step %d (skip): install has no entry for os=%s\n", i, osKind)
				continue
			}
			kind, cmd = "install", rt.substitute(pick)
			argv = transport.WrapShell(t, wrapForExec(t, cmd, workdir, env))
		case len(s.Exec) > 0:
			// exec is intentionally raw argv; only substitute each element,
			// don't shell-wrap, don't apply workdir/env (use run for that).
			kind = "exec"
			argv = make([]string, len(s.Exec))
			for j, a := range s.Exec {
				argv[j] = rt.substitute(a)
			}
			cmd = strings.Join(argv, " ")
		case s.Run != "":
			kind, cmd = "run", rt.substitute(s.Run)
			argv = transport.WrapShell(t, wrapForExec(t, cmd, workdir, env))
		case s.Assert != "":
			kind, cmd = "assert", rt.substitute(s.Assert)
			argv = transport.WrapShell(t, wrapForExec(t, cmd, workdir, env))
		default:
			return results, fmt.Errorf("step %d: must set run, assert, exec, install, or artifact", i)
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

// pickInstall resolves the install map for the target's OS, accepting
// "mac" as an alias for "darwin" so YAML stays human.
func pickInstall(m map[string]string, osKind string) (string, bool) {
	if v, ok := m[osKind]; ok && v != "" {
		return v, true
	}
	if osKind == "darwin" {
		if v, ok := m["mac"]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// matchWhen evaluates a comma-separated AND list of key=value / key!=value
// clauses. Supported keys: `os`, `arch`. Unknown keys return an error so
// typos surface fast instead of silently making the step always-skip.
func matchWhen(expr, osKind, arch string) (bool, error) {
	for _, raw := range strings.Split(expr, ",") {
		clause := strings.TrimSpace(raw)
		if clause == "" {
			continue
		}
		neg := false
		sep := "="
		if idx := strings.Index(clause, "!="); idx >= 0 {
			neg = true
			sep = "!="
			_ = sep
		}
		var key, want string
		if neg {
			parts := strings.SplitN(clause, "!=", 2)
			key, want = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		} else {
			parts := strings.SplitN(clause, "=", 2)
			if len(parts) != 2 {
				return false, fmt.Errorf("clause %q: expected key=value or key!=value", clause)
			}
			key, want = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
		var actual string
		switch key {
		case "os":
			actual = osKind
			if want == "mac" || want == "macos" {
				want = "darwin"
			}
		case "arch":
			actual = arch
		default:
			return false, fmt.Errorf("clause %q: unknown key (allowed: os, arch)", clause)
		}
		eq := actual == want
		if neg && eq {
			return false, nil
		}
		if !neg && !eq {
			return false, nil
		}
	}
	return true, nil
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
