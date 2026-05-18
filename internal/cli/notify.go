package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/notify"
	"github.com/spf13/cobra"
)

// newNotifyCmd groups notifier-side subcommands. Today: `vmlab notify test`,
// which loads the active notify config and posts a synthetic event for every
// configured phase so the user can verify the channel works end-to-end.
func newNotifyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "notify",
		Short: "Inspect and test notifier configuration",
	}
	c.AddCommand(newNotifyTestCmd())
	return c
}

func newNotifyTestCmd() *cobra.Command {
	var (
		phase    string
		instance string
	)
	c := &cobra.Command{
		Use:   "test",
		Short: "Post a synthetic event to every configured notifier",
		Long: `Loads the notify config from ~/.vmlab/config.yaml (+ repo overrides),
resolves any op:// or env: references, and posts one or more synthetic events
so you can verify Discord (or any future channel) is wired correctly.

Use --phase=all (default) to fire start + success + failure in sequence.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, paths, err := config.Load()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			m, err := notify.Load(ctx, []string{
				paths.UserFile,
				paths.RepoFile,
				filepath.Join(paths.RepoDir, "config.yaml"),
			})
			if err != nil {
				return err
			}
			if m == nil || len(m.Notifiers) == 0 {
				return fmt.Errorf("no notifiers configured (add a `notify:` block to %s)", paths.UserFile)
			}

			phases := []notify.Phase{notify.PhaseStart, notify.PhaseSuccess, notify.PhaseFailure}
			if phase != "" && phase != "all" {
				p, ok := notify.ParsePhase(phase)
				if !ok {
					return fmt.Errorf("unknown phase: %q (start|success|failure|all)", phase)
				}
				phases = []notify.Phase{p}
			}

			for _, p := range phases {
				ev := syntheticEvent(p, instance)
				m.Notify(ctx, ev)
				fmt.Fprintf(cmd.OutOrStdout(), "sent: %s\n", p)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "done.")
			return nil
		},
	}
	c.Flags().StringVar(&phase, "phase", "all", "start|success|failure|all")
	c.Flags().StringVar(&instance, "instance", "demo", "instance name used in the synthetic event")
	return c
}

func syntheticEvent(p notify.Phase, instance string) notify.Event {
	ev := notify.Event{
		Phase:    p,
		Instance: instance,
		Provider: "smoke-test",
		Selector: "@" + instance,
		Cmd:      "echo hello",
		RunID:    "smoke-" + time.Now().UTC().Format("20060102T150405"),
	}
	switch p {
	case notify.PhaseSuccess:
		ev.UpMs = 1200
		ev.RunMs = 240
		ev.DownMs = 80
	case notify.PhaseFailure:
		ev.UpMs = 1200
		ev.RunMs = 80
		ev.DownMs = 90
		ev.ExitCode = 1
		ev.Err = "synthetic failure for notifier smoke-test"
		ev.StderrTail = "step 1: install -> ok\nstep 2: smoke -> FAIL\nexit status 1"
	}
	return ev
}
