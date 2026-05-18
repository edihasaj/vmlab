package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/state"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "up <instance>",
		Short: "Ensure an instance is running and ready (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, inst, err := resolveInstance(args[0])
			if err != nil {
				return err
			}
			lock, err := acquireInstanceLock(cmd, inst.Name)
			if err != nil {
				return err
			}
			defer lock.Release()
			tgt, res, err := pr.Up(cmd.Context(), inst)
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"instance":    inst.Name,
					"provider":    inst.Provider,
					"target":      tgt.Name,
					"transport":   tgt.Transport,
					"changed":     res.Changed,
					"prior_state": res.PriorState.String(),
					"reason":      res.Reason,
					"error":       errString(err),
				})
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "up: %s (provider=%s, transport=%s)\n  prior=%s changed=%v reason=%s\n",
				inst.Name, inst.Provider, tgt.Transport, res.PriorState.String(), res.Changed, res.Reason)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func newDownCmd() *cobra.Command {
	var (
		dispose string
		asJSON  bool
	)
	c := &cobra.Command{
		Use:   "down <instance>",
		Short: "Dispose of an instance (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, inst, err := resolveInstance(args[0])
			if err != nil {
				return err
			}
			lock, err := acquireInstanceLock(cmd, inst.Name)
			if err != nil {
				return err
			}
			defer lock.Release()
			d, err := resolveDispose(dispose, inst.Disp.OnSuccess, provider.DisposeSuspend)
			if err != nil {
				return err
			}
			err = pr.Down(cmd.Context(), inst, d)
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"instance": inst.Name,
					"dispose":  d.String(),
					"error":    errString(err),
				})
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "down: %s (dispose=%s)\n", inst.Name, d.String())
			return nil
		},
	}
	c.Flags().StringVar(&dispose, "dispose", "", "keep|suspend|poweroff|destroy (defaults to instance disposition.on_success)")
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func newWithCmd() *cobra.Command {
	var (
		dispose    string
		timeout    time.Duration
		noEvidence bool
		noNotify   bool
	)
	c := &cobra.Command{
		Use:   "with <instance> -- <cmd>...",
		Short: "Bring up an instance, run a command on it, then restore prior state",
		Long: `vmlab with brings an instance up (idempotent), runs <cmd> via the instance's
target transport, then restores prior power state. If we resumed/started the
VM we suspend/dispose it on exit; if it was already running we leave it.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			instArg := args[0]
			rest := stripDashDash(args[1:])
			if len(rest) == 0 {
				return fmt.Errorf("with: missing command after --")
			}
			pr, instance, err := resolveInstance(instArg)
			if err != nil {
				return err
			}
			cfg, paths, err := config.Load()
			if err != nil {
				return err
			}
			_ = cfg
			lock, err := acquireInstanceLockAt(cmd, paths, instance.Name)
			if err != nil {
				return err
			}
			defer lock.Release()
			var run *evidence.Run
			if !noEvidence {
				if err := config.EnsureDirs(paths); err != nil {
					return err
				}
				run, err = evidence.New(paths.RunsDir)
				if err != nil {
					return err
				}
				run.SetCmd(joinArgs(rest))
				run.SetSelector("@" + instance.Name)
			}

			nfy := loadNotifier(cmd, paths, noNotify, instance, "@"+instance.Name, joinArgs(rest), run)
			nfy.Start()

			// up
			upStart := time.Now()
			tgt, res, upErr := pr.Up(cmd.Context(), instance)
			upMs := time.Since(upStart).Milliseconds()
			if run != nil {
				_ = run.WriteFile("status-before.txt", []byte(res.PriorState.String()+"\n"))
			}
			if upErr != nil {
				finishLifecycle(run, instance, res, "", upMs, 0, 0, upErr)
				nfy.Finish(upMs, 0, 0, 1, upErr)
				return upErr
			}

			restoreOnSuccess, err := resolveDispose(dispose, instance.Disp.OnSuccess, provider.DisposeSuspend)
			if err != nil {
				return err
			}
			restoreOnFailure, err := resolveDispose(dispose, instance.Disp.OnFailure, restoreOnSuccess)
			if err != nil {
				return err
			}
			only := instance.Disp.OnlyIfWeStarted || dispose == ""

			// run
			reg := transport.Default()
			tr, err := reg.Get(tgt.Transport)
			if err != nil {
				downMs, _ := cleanupAndFinish(cmd, run, pr, instance, res, only, restoreOnFailure, upMs, 0, err)
				nfy.Finish(upMs, 0, downMs, 1, err)
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			var teeOut, teeErr io.Writer = cmd.OutOrStdout(), cmd.ErrOrStderr()
			var logs *evidence.TargetLogs
			if run != nil {
				o, e, l, lerr := run.TargetWriters(instance.Name, cmd.OutOrStdout(), cmd.ErrOrStderr())
				if lerr != nil {
					return lerr
				}
				teeOut, teeErr, logs = o, e, l
				defer logs.Close()
			}
			runStart := time.Now()
			result, runErr := tr.Run(ctx, tgt, rest, teeOut, teeErr)
			runMs := time.Since(runStart).Milliseconds()

			// cleanup + finalise
			downMs, downErr := cleanupAndFinish(cmd, run, pr, instance, res, only, pickDispose(runErr, result.ExitCode, restoreOnSuccess, restoreOnFailure), upMs, runMs, runErr)
			_ = downErr // already logged inside helper

			exit := result.ExitCode
			if runErr != nil && exit == 0 {
				exit = 1
			}
			if logs != nil {
				_ = logs.Close()
			}
			if run != nil {
				run.AddTarget(evidence.TargetSummary{
					Name:      instance.Name,
					Transport: tgt.Transport,
					ExitCode:  exit,
					Duration:  runMs,
					Error:     errString(runErr),
				})
				meta, _ := run.Finish(exit)
				fmt.Fprintf(cmd.ErrOrStderr(), "\nrun-id: %s (%s) up=%dms run=%dms down=%dms\n",
					meta.ID, run.Dir, upMs, runMs, downMs)
			}
			nfy.Finish(upMs, runMs, downMs, exit, runErr)
			if runErr != nil {
				return runErr
			}
			if result.ExitCode != 0 {
				os.Exit(result.ExitCode)
			}
			return nil
		},
	}
	c.Flags().StringVar(&dispose, "dispose", "", "keep|suspend|poweroff|destroy (defaults to instance disposition)")
	c.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "max time for the inner command")
	c.Flags().BoolVar(&noEvidence, "no-evidence", false, "skip writing an evidence bundle")
	c.Flags().BoolVar(&noNotify, "no-notify", false, "skip configured notifiers (Discord etc.)")
	return c
}

// pickDispose returns the disposition to apply after the inner command, based
// on success/failure.
func pickDispose(runErr error, exit int, onSuccess, onFailure provider.Dispose) provider.Dispose {
	if runErr != nil || exit != 0 {
		return onFailure
	}
	return onSuccess
}

// cleanupAndFinish runs Down (respecting only_if_we_started) and writes the
// post-state snapshot + lifecycle summary into the evidence run. Returns the
// down-phase duration and any cleanup error (also logged to stderr).
func cleanupAndFinish(cmd *cobra.Command, run *evidence.Run, pr provider.Provider, inst provider.Instance, res provider.EnsureResult, only bool, want provider.Dispose, upMs, runMs int64, runErr error) (int64, error) {
	if only && !res.Changed {
		fmt.Fprintf(cmd.ErrOrStderr(), "with: leaving %s as-is (was already running)\n", inst.Name)
		want = provider.DisposeKeep
	}
	downStart := time.Now()
	var downErr error
	if want != provider.DisposeKeep {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		downErr = pr.Down(ctx, inst, want)
		cancel()
		if downErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "with: cleanup (%s) failed: %v\n", want, downErr)
		}
	}
	downMs := time.Since(downStart).Milliseconds()
	if run != nil {
		postCtx, postCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if st, sErr := pr.Status(postCtx, inst); sErr == nil {
			_ = run.WriteFile("status-after.txt", []byte(st.String()+"\n"))
		}
		postCancel()
	}
	finishLifecycle(run, inst, res, want.String(), upMs, runMs, downMs, runErr)
	return downMs, downErr
}

func finishLifecycle(run *evidence.Run, inst provider.Instance, res provider.EnsureResult, disposeStr string, upMs, runMs, downMs int64, runErr error) {
	if run == nil {
		return
	}
	run.SetLifecycle(evidence.LifecycleSummary{
		Instance:   inst.Name,
		Provider:   inst.Provider,
		PriorState: res.PriorState.String(),
		Changed:    res.Changed,
		Reason:     res.Reason,
		Dispose:    disposeStr,
		UpMs:       upMs,
		RunMs:      runMs,
		DownMs:     downMs,
		Error:      errString(runErr),
	})
}

// acquireInstanceLock takes a lock on the named instance, loading paths from
// config. Prints a one-line wait notice on contention.
func acquireInstanceLock(cmd *cobra.Command, name string) (*state.Lock, error) {
	_, paths, err := config.Load()
	if err != nil {
		return nil, err
	}
	return acquireInstanceLockAt(cmd, paths, name)
}

// acquireInstanceLockAt is the same but skips a second config.Load when the
// caller already has Paths in hand.
func acquireInstanceLockAt(cmd *cobra.Command, paths config.Paths, name string) (*state.Lock, error) {
	if err := config.EnsureDirs(paths); err != nil {
		return nil, err
	}
	return state.Acquire(paths.StateDir, name, func(pid string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "waiting for instance lock on %q (held by pid %s)…\n", name, pid)
	})
}

func joinArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := args[0]
	for _, a := range args[1:] {
		out += " " + a
	}
	return out
}

// resolveInstance loads config + the named instance, returning the matching
// provider implementation alongside it.
func resolveInstance(name string) (provider.Provider, provider.Instance, error) {
	_, p, err := config.Load()
	if err != nil {
		return nil, provider.Instance{}, err
	}
	r, err := provider.LoadInstances(p)
	if err != nil {
		return nil, provider.Instance{}, err
	}
	inst, ok := r.Get(name)
	if !ok {
		return nil, provider.Instance{}, fmt.Errorf("unknown instance: %s", name)
	}
	reg := provider.Default()
	pr, err := reg.Get(inst.Provider)
	if err != nil {
		return nil, inst, err
	}
	return pr, inst, nil
}

func resolveDispose(flag, fromInstance string, fallback provider.Dispose) (provider.Dispose, error) {
	if flag != "" {
		return provider.ParseDispose(flag)
	}
	if fromInstance != "" {
		return provider.ParseDispose(fromInstance)
	}
	return fallback, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

