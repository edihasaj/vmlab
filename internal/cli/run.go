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
	)
	c := &cobra.Command{
		Use:   "run <selector> <flow-or-cmd>...",
		Short: "Run a flow or shell command against one or more targets",
		Long: `Run a flow YAML or arbitrary shell command across the targets matched by <selector>.

Examples:
  vmlab run ubuntu-local -- uname -a
  vmlab run @linux flows/install.yaml
  vmlab run all -- ./scripts/smoke.sh
  vmlab run @linux,not:@ci flows/install.yaml --max-parallel 4`,
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
					res, err := tr.Run(ctx, t, []string{"sh", "-lc", cmdLine}, teeOut, teeErr)
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
				fmt.Fprintf(cmd.ErrOrStderr(), "\nrun-id: %s (%s)\n", meta.ID, run.Dir)
			}

			if asJSON {
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
	return c
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
