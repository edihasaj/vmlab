package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/provider"
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
		dispose string
		timeout time.Duration
	)
	c := &cobra.Command{
		Use:   "with <instance> -- <cmd>...",
		Short: "Bring up an instance, run a command on it, then restore prior state",
		Long: `vmlab with brings an instance up (idempotent), runs <cmd> via the instance's
target transport, then restores prior power state. If we resumed/started the
VM we suspend/dispose it on exit; if it was already running we leave it.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst := args[0]
			rest := stripDashDash(args[1:])
			if len(rest) == 0 {
				return fmt.Errorf("with: missing command after --")
			}
			pr, instance, err := resolveInstance(inst)
			if err != nil {
				return err
			}
			tgt, res, upErr := pr.Up(cmd.Context(), instance)
			if upErr != nil {
				return upErr
			}
			// disposition resolution: --dispose wins; else fall through to
			// instance.disposition; default keep when user-was-already-running.
			restoreOnSuccess, err := resolveDispose(dispose, instance.Disp.OnSuccess, provider.DisposeSuspend)
			if err != nil {
				return err
			}
			restoreOnFailure, err := resolveDispose(dispose, instance.Disp.OnFailure, restoreOnSuccess)
			if err != nil {
				return err
			}
			only := instance.Disp.OnlyIfWeStarted || dispose == ""
			cleanup := func(runErr error) {
				if only && !res.Changed {
					fmt.Fprintf(cmd.ErrOrStderr(), "with: leaving %s as-is (was already running)\n", instance.Name)
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				want := restoreOnSuccess
				if runErr != nil {
					want = restoreOnFailure
				}
				if err := pr.Down(ctx, instance, want); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "with: cleanup (%s) failed: %v\n", want, err)
				}
			}
			reg := transport.Default()
			tr, err := reg.Get(tgt.Transport)
			if err != nil {
				cleanup(err)
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			result, runErr := tr.Run(ctx, tgt, rest, cmd.OutOrStdout(), cmd.ErrOrStderr())
			cleanup(runErr)
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
	return c
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

