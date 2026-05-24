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
//
//   - assert  — same wrapping but failures stop the flow with a clear message.
//
//   - exec    — argv passed directly to the transport, no shell wrapping.
//     Use this for Windows guests (e.g. `["cmd.exe", "/c", "ver"]`) or
//     anywhere `sh` is not available.
//
//   - install — per-OS map of shell lines. The flow runner picks the
//     line that matches the target's OSKind and treats it as `run`.
//     Lets one flow describe "install jq" without forking per OS:
//
//   - install:
//     mac:     brew install jq
//     linux:   sudo apt-get install -y jq
//     windows: choco install -y jq
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

	// Background fires the command and returns immediately. Use for daemons
	// you want to probe (and later kill) from subsequent steps. Wraps the
	// command via `start "" /B cmd /c "<cmd>"` on Windows and `<cmd> &` on
	// POSIX so the transport's Run returns as soon as the child is spawned.
	// The step is recorded as `run (bg)` in evidence. The caller is
	// responsible for cleanup — pair with a `taskkill` / `pkill` step or a
	// PID file. Only meaningful on `run:`; ignored elsewhere.
	Background bool `yaml:"background,omitempty"`

	// GUI is a structured desktop UI action dispatched via the target's
	// transport.GUI(). Lets a flow drive guiport (or any future GUI
	// transport) without falling back to free-form shell. Mutually
	// exclusive with run/assert/exec/install/artifact/sync.
	GUI *GUIStep `yaml:"gui,omitempty"`
}

// GUIStep is the YAML form of transport.GUIAction. Only `kind` is required;
// each kind reads the fields it needs and ignores the rest. Variable
// substitution applies to selector, text, and path.
//
// Supported kinds map to guiport CLI verbs:
//
//   - click        — click an AX selector (role[attr=value])
//   - click-text   — click the first element whose visible text matches
//   - click-at     — click absolute screen coords x,y (in `extra`)
//   - type         — type the given text (optional --into selector)
//   - hotkey       — press a chord (e.g. "cmd+shift+4")
//   - screenshot   — capture the screen to `path`
//   - observe      — emit the frontmost-app summary
//   - tree         — dump the AX tree of the frontmost app
//   - wait         — sleep for `ms` (in `extra`)
//   - run          — execute a guiport YAML flow at `path`
type GUIStep struct {
	Kind     string         `yaml:"kind"`
	Selector string         `yaml:"selector,omitempty"`
	Text     string         `yaml:"text,omitempty"`
	Path     string         `yaml:"path,omitempty"`
	Extra    map[string]any `yaml:"extra,omitempty"`
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

		// GUI step dispatches to the transport's GUI() with a structured
		// action. The transport (today: guiport) decides how to talk to the
		// underlying tool. No shell wrap, no workdir/env — keep the contract
		// thin so it works the same way on any future GUI transport.
		if s.GUI != nil {
			if s.GUI.Kind == "" {
				return results, fmt.Errorf("step %d: gui.kind is required", i)
			}
			action := transport.GUIAction{
				Kind:     rt.substitute(s.GUI.Kind),
				Selector: rt.substitute(s.GUI.Selector),
				Text:     rt.substitute(s.GUI.Text),
				Path:     rt.substitute(s.GUI.Path),
				Extra:    s.GUI.Extra,
			}
			label := "gui:" + action.Kind
			start := time.Now()
			slog.Debug("flow step", "target", t.Name, "index", i, "kind", label, "selector", action.Selector, "path", action.Path)
			fmt.Fprintf(stderr, "step %d (%s): selector=%q text=%q path=%q\n", i, label, action.Selector, oneLine(action.Text), action.Path)
			err := tr.GUI(ctx, t, action)
			dur := time.Since(start)
			sr := StepResult{Index: i, Kind: label, Cmd: action.Kind, Name: s.Name, DurationMs: dur.Milliseconds()}
			if err != nil {
				sr.ExitCode = 1
				sr.Error = err.Error()
				results = append(results, sr)
				return results, err
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
			line := wrapForExec(t, cmd, workdir, env)
			if s.Background {
				line = wrapForBackground(t, line)
				kind = "run (bg)"
			}
			argv = transport.WrapShell(t, line)
		case s.Assert != "":
			kind, cmd = "assert", rt.substitute(s.Assert)
			argv = transport.WrapShell(t, wrapForExec(t, cmd, workdir, env))
		default:
			return results, fmt.Errorf("step %d: must set run, assert, exec, install, artifact, sync, or gui", i)
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
// clauses. Supported keys: `os`, `arch`, `env`. Unknown keys return an
// error so typos surface fast instead of silently making the step
// always-skip.
//
// The `env` key is special-cased: `env=NAME` is true iff $NAME is set to a
// non-empty value on the host; `env!=NAME` is the inverse. Use it to gate
// steps that need a host-side opt-in (e.g. a step that produces a real
// screenshot and only makes sense once Screen Recording TCC is granted).
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
		case "env":
			// `env=NAME` matches when $NAME is set to a non-empty value.
			// Treat "set/non-empty" as the actual value the equality below
			// checks against — that way `env=NAME` (truthy check) and
			// `env!=NAME` (must be unset) both work via the same code path.
			present := os.Getenv(want) != ""
			if present {
				actual = want
			}
			// We've already encoded the comparison in `present`; short-circuit
			// the generic eq path below so the user's `want` doesn't have to
			// equal itself by accident.
			eq := present
			if neg && eq {
				return false, nil
			}
			if !neg && !eq {
				return false, nil
			}
			continue
		default:
			return false, fmt.Errorf("clause %q: unknown key (allowed: os, arch, env)", clause)
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
