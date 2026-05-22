package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/fleet"
	"github.com/edihasaj/vmlab/internal/flow"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var (
		maxParallel     int
		failFast        bool
		continueOnError bool
		asJSON          bool
		noEvidence      bool
		noNotify        bool
		dryRun          bool
		retries         int
		fromSnapshot    string
		format          string
	)
	c := &cobra.Command{
		Use:   "run <selector> <flow-or-cmd>...",
		Short: "Run a flow or shell command against one or more targets",
		Long: `Run a flow YAML or arbitrary shell command across the targets matched by <selector>.

Selector forms:
  <name>           — a transport target
  @<tag>           — every transport target with that tag
  @<instance>      — single configured instance (lifecycle-wrapped)
  @@<tag>          — every configured instance with that tag (parallel fan-out)
  all              — every transport target

Examples:
  vmlab run ubuntu-local -- uname -a
  vmlab run @linux flows/install.yaml
  vmlab run @win11-studio -- cmd.exe /c ver        # single instance, lifecycle
  vmlab run @@linux flows/smoke.yaml               # fan out across all linux instances
  vmlab run @@mobile flows/test.yaml --max-parallel 2 --fail-fast
  vmlab run all -- ./scripts/smoke.sh`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(p); err != nil {
				return err
			}

			selectorArg := args[0]
			rest := args[1:]
			rest = stripDashDash(rest)

			// @instance shortcut — when the selector resolves to a single
			// provider instance, lifecycle-wrap the run. Existing target
			// selectors (`@tag`, `<name>`, `all`) keep their meaning when no
			// instance matches; instance wins on exact `@<name>` collision.
			if name, ok := instanceShortcut(selectorArg, p); ok {
				return runInstance(cmd, p, name, rest, dryRun, asJSON, noEvidence, noNotify, retries, fromSnapshot, format)
			}
			if insts, ok := instanceClassShortcut(selectorArg, p); ok {
				return runInstanceFleet(cmd, p, selectorArg, insts, rest,
					dryRun, asJSON, noEvidence, noNotify,
					maxParallel, failFast, continueOnError, retries, fromSnapshot, format)
			}
			if fromSnapshot != "" {
				return fmt.Errorf("--from-snapshot requires @<instance> or @@<tag> selector (got %q)", selectorArg)
			}

			r, err := target.Load(p)
			if err != nil {
				return err
			}
			ts, err := target.NewSelector(selectorArg).Resolve(r)
			if err != nil {
				return err
			}
			if len(ts) == 0 {
				return fmt.Errorf("selector %q matched no targets", selectorArg)
			}

			var loadedFlow *flow.Flow
			var cmdLine string
			if len(rest) == 1 && flow.LooksLikeFlowPath(rest[0]) {
				loadedFlow, err = flow.Load(rest[0])
				if err != nil {
					return err
				}
			} else {
				cmdLine = strings.Join(rest, " ")
			}

			if dryRun {
				return printPlan(cmd.OutOrStdout(), selectorArg, ts, loadedFlow, cmdLine, asJSON)
			}

			reg := transport.Default()
			var run *evidence.Run
			if !noEvidence {
				run, err = evidence.New(p.RunsDir)
				if err != nil {
					return err
				}
				if loadedFlow != nil {
					run.SetFlow(loadedFlow.SourceFile)
				} else {
					run.SetCmd(cmdLine)
				}
				run.SetSelector(selectorArg)
			}

			if maxParallel == 0 {
				maxParallel = cfg.DefaultMaxParallel
			}
			opts := fleet.Options{
				MaxParallel:     maxParallel,
				FailFast:        failFast,
				ContinueOnError: continueOnError,
			}

			outcomes, runErr := fleet.Run(cmd.Context(), ts, reg, opts, cmd.OutOrStdout(), cmd.ErrOrStderr(),
				func(ctx context.Context, t target.Target, tr transport.Transport, stdout, stderr io.Writer) (int, error) {
					var teeOut, teeErr io.Writer = stdout, stderr
					var logs *evidence.TargetLogs
					if run != nil {
						o, e, l, err := run.TargetWriters(t.Name, stdout, stderr)
						if err != nil {
							return 0, err
						}
						teeOut, teeErr, logs = o, e, l
						defer logs.Close()
					}
					if loadedFlow != nil {
						steps, err := flow.Run(ctx, tr, t, loadedFlow, teeOut, teeErr)
						if run != nil {
							run.WriteSteps(t.Name, steps)
						}
						if err != nil {
							return lastExit(steps, err), err
						}
						return 0, nil
					}
					res, err := tr.Run(ctx, t, transport.WrapShell(t, cmdLine), teeOut, teeErr)
					return res.ExitCode, err
				})

			exit := fleet.AggregateExit(outcomes)
			if run != nil {
				for _, o := range outcomes {
					sum := evidence.TargetSummary{
						Name:      o.Target.Name,
						Transport: o.Target.Transport,
						ExitCode:  o.ExitCode,
						Duration:  o.Duration.Milliseconds(),
					}
					if o.Error != nil {
						sum.Error = o.Error.Error()
					}
					run.AddTarget(sum)
				}
				meta, _ := run.Finish(exit)
				if _, err := run.WriteJUnit(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "junit: %v\n", err)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "\nrun-id: %s (%s)\n", meta.ID, run.Dir)
			}

			if isMatrixFormat(format) {
				rows := make([]MatrixRow, 0, len(outcomes))
				runDir := ""
				if run != nil {
					runDir = run.Dir
				}
				for _, o := range outcomes {
					row := MatrixRow{
						Target:     o.Target.Name,
						Transport:  o.Target.Transport,
						Status:     "pass",
						ExitCode:   o.ExitCode,
						DurationMs: o.Duration.Milliseconds(),
					}
					if o.ExitCode != 0 || o.Error != nil {
						row.Status = "fail"
						if o.Error != nil {
							row.Error = o.Error.Error()
						}
						row.Tail = tailStderr(runDir, o.Target.Name)
					}
					rows = append(rows, row)
				}
				_ = emitMatrix(cmd.OutOrStdout(), rows)
			} else if asJSON {
				type row struct {
					Target     string `json:"target"`
					ExitCode   int    `json:"exitCode"`
					Error      string `json:"error,omitempty"`
					DurationMs int64  `json:"durationMs"`
				}
				rows := make([]row, 0, len(outcomes))
				for _, o := range outcomes {
					r := row{Target: o.Target.Name, ExitCode: o.ExitCode, DurationMs: o.Duration.Milliseconds()}
					if o.Error != nil {
						r.Error = o.Error.Error()
					}
					rows = append(rows, r)
				}
				_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"results": rows,
					"exit":    exit,
				})
			}

			if runErr != nil || exit != 0 {
				if exit == 0 {
					exit = 1
				}
				os.Exit(exit)
			}
			return nil
		},
	}
	c.Flags().IntVar(&maxParallel, "max-parallel", 0, "max concurrent targets (0 = all)")
	c.Flags().BoolVar(&failFast, "fail-fast", false, "stop launching new work on first failure")
	c.Flags().BoolVar(&continueOnError, "continue-on-error", false, "ignore failures and run every target")
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON summary at the end")
	c.Flags().BoolVar(&noEvidence, "no-evidence", false, "skip writing an evidence bundle")
	c.Flags().BoolVar(&noNotify, "no-notify", false, "skip configured notifiers (Discord etc.)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan (targets + steps) without executing")
	c.Flags().IntVar(&retries, "retries", 0, "retry the inner run on failure (only for @<instance>; lifecycle Up/Down run once)")
	c.Flags().StringVar(&fromSnapshot, "from-snapshot", "", "restore named snapshot before Up (only for @<instance> / @@<tag>; provider must support snapshots)")
	c.Flags().StringVar(&format, "format", "", "compact output: matrix (newline-delimited JSON, one row per target/instance, stderr tail on failure)")
	return c
}

func printPlan(w io.Writer, selectorArg string, ts []target.Target, f *flow.Flow, cmdLine string, asJSON bool) error {
	type planTarget struct {
		Name      string   `json:"name"`
		Transport string   `json:"transport"`
		Tags      []string `json:"tags,omitempty"`
	}
	type planStep struct {
		Index int    `json:"index"`
		Kind  string `json:"kind"`
		Cmd   string `json:"cmd"`
	}
	plan := struct {
		Selector string       `json:"selector"`
		Flow     string       `json:"flow,omitempty"`
		Command  string       `json:"command,omitempty"`
		Targets  []planTarget `json:"targets"`
		Steps    []planStep   `json:"steps,omitempty"`
	}{Selector: selectorArg}
	for _, t := range ts {
		plan.Targets = append(plan.Targets, planTarget{Name: t.Name, Transport: t.Transport, Tags: t.Tags})
	}
	if f != nil {
		plan.Flow = f.SourceFile
		for i, s := range f.Steps {
			kind, cmd := "run", s.Run
			if s.Assert != "" {
				kind, cmd = "assert", s.Assert
			}
			plan.Steps = append(plan.Steps, planStep{Index: i, Kind: kind, Cmd: cmd})
		}
	} else {
		plan.Command = cmdLine
	}
	if asJSON {
		return json.NewEncoder(w).Encode(plan)
	}
	fmt.Fprintf(w, "dry-run plan (selector=%q)\n", plan.Selector)
	if plan.Flow != "" {
		fmt.Fprintf(w, "flow:    %s\n", plan.Flow)
	}
	if plan.Command != "" {
		fmt.Fprintf(w, "command: %s\n", plan.Command)
	}
	fmt.Fprintf(w, "targets (%d):\n", len(plan.Targets))
	for _, t := range plan.Targets {
		fmt.Fprintf(w, "  - %-20s %-10s [%s]\n", t.Name, t.Transport, strings.Join(t.Tags, ","))
	}
	if len(plan.Steps) > 0 {
		fmt.Fprintf(w, "steps (%d):\n", len(plan.Steps))
		for _, s := range plan.Steps {
			fmt.Fprintf(w, "  %d. %-6s %s\n", s.Index, s.Kind, s.Cmd)
		}
	}
	return nil
}

func stripDashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

func lastExit(steps []flow.StepResult, err error) int {
	if len(steps) > 0 {
		last := steps[len(steps)-1]
		if last.ExitCode != 0 {
			return last.ExitCode
		}
	}
	if err != nil {
		return 1
	}
	return 0
}
