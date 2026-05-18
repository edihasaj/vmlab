// Package hooks runs declarative pre/post-lifecycle steps for an instance.
//
// A Step runs either on the orchestrator host (run / exec) or inside the
// guest via the active Transport (target / target_exec). Env values may
// reference credresolver URIs (op://, env:) and are resolved per-step,
// never persisted to disk.
package hooks

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/edihasaj/vmlab/internal/credresolver"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// Phase tags a hook list so callers can address them by name.
type Phase string

const (
	PhasePreUp   Phase = "pre_up"
	PhasePostUp  Phase = "post_up"
	PhasePreDown Phase = "pre_down"
)

// Step is a single declarative action. Exactly one of run / exec / target /
// target_exec must be set; the rest are ignored.
//
//	hooks:
//	  pre_up:
//	    - name: prep shared dir
//	      run: rsync -a ./assets/ studio:/Users/edi/share/
//	  post_up:
//	    - name: install node
//	      target_exec: ["sudo", "apt-get", "install", "-y", "nodejs"]
//	    - target: ./scripts/bootstrap.sh
//	      env:
//	        APP_TOKEN: op://Personal/myapp/credential
//	  pre_down:
//	    - target: rm -rf /tmp/build-cache
type Step struct {
	Name       string            `yaml:"name,omitempty"`
	Run        string            `yaml:"run,omitempty"`         // host: sh -lc <line>
	Exec       []string          `yaml:"exec,omitempty"`        // host: direct argv
	Target     string            `yaml:"target,omitempty"`      // guest: sh -lc <line> via transport
	TargetExec []string          `yaml:"target_exec,omitempty"` // guest: argv via transport
	Env        map[string]string `yaml:"env,omitempty"`         // resolved per step
	IgnoreFail bool              `yaml:"ignore_failure,omitempty"`
}

// Config bundles the three lifecycle phases. Empty phases are no-ops.
type Config struct {
	PreUp   []Step `yaml:"pre_up,omitempty"`
	PostUp  []Step `yaml:"post_up,omitempty"`
	PreDown []Step `yaml:"pre_down,omitempty"`
}

// Empty reports whether the config has no steps in any phase.
func (c Config) Empty() bool { return len(c.PreUp)+len(c.PostUp)+len(c.PreDown) == 0 }

// Steps returns the slice for a phase or nil.
func (c Config) Steps(p Phase) []Step {
	switch p {
	case PhasePreUp:
		return c.PreUp
	case PhasePostUp:
		return c.PostUp
	case PhasePreDown:
		return c.PreDown
	}
	return nil
}

// Runner executes hook steps. Transport+Target are optional and only required
// when a hook uses target/target_exec.
type Runner struct {
	Transport transport.Transport
	Target    target.Target
	Stdout    io.Writer
	Stderr    io.Writer
}

// Run executes every step in steps. Stops on the first error unless the step
// has IgnoreFail=true. The returned error wraps the original with the step's
// label so failures are diagnosable in evidence.
func (r *Runner) Run(ctx context.Context, phase Phase, steps []Step) error {
	for i, s := range steps {
		label := s.Name
		if label == "" {
			label = fmt.Sprintf("%s[%d]", phase, i)
		}
		if r.Stdout != nil {
			fmt.Fprintf(r.Stdout, "[hook %s] %s\n", phase, label)
		}
		if err := r.runOne(ctx, s); err != nil {
			if s.IgnoreFail {
				if r.Stderr != nil {
					fmt.Fprintf(r.Stderr, "[hook %s] %s: ignored: %v\n", phase, label, err)
				}
				continue
			}
			return fmt.Errorf("hook %s/%s: %w", phase, label, err)
		}
	}
	return nil
}

func (r *Runner) runOne(ctx context.Context, s Step) error {
	env, err := resolveEnv(ctx, s.Env)
	if err != nil {
		return err
	}
	switch {
	case s.Run != "":
		return r.execHost(ctx, []string{"sh", "-lc", s.Run}, env)
	case len(s.Exec) > 0:
		return r.execHost(ctx, s.Exec, env)
	case s.Target != "":
		return r.execTarget(ctx, []string{"sh", "-lc", s.Target}, env)
	case len(s.TargetExec) > 0:
		return r.execTarget(ctx, s.TargetExec, env)
	default:
		return fmt.Errorf("step has no action (run/exec/target/target_exec)")
	}
}

func (r *Runner) execHost(ctx context.Context, argv []string, env map[string]string) error {
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Env = mergeEnv(os.Environ(), env)
	c.Stdout = r.Stdout
	c.Stderr = r.Stderr
	return c.Run()
}

// execTarget runs argv inside the guest. The Transport does not yet accept
// per-step env vars, so any Env on the step is exported by prefixing argv
// with `env K=V K2=V2 -- argv...`. Works for POSIX guests; Windows guests
// should prefer Target shell lines that include their own `set` invocations.
func (r *Runner) execTarget(ctx context.Context, argv []string, env map[string]string) error {
	if r.Transport == nil {
		return fmt.Errorf("no transport: target hooks require an active Up()")
	}
	if len(env) > 0 {
		prefix := []string{"env"}
		for k, v := range env {
			prefix = append(prefix, k+"="+v)
		}
		prefix = append(prefix, "--")
		argv = append(prefix, argv...)
	}
	_, err := r.Transport.Run(ctx, r.Target, argv, r.Stdout, r.Stderr)
	return err
}

func resolveEnv(ctx context.Context, in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		resolved, err := credresolver.Resolve(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

func mergeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for k, v := range overlay {
		out = append(out, k+"="+v)
	}
	return out
}
