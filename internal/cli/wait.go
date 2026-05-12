package cli

import (
	"fmt"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/spf13/cobra"
)

func newWaitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "wait <instance>",
		Short: "Wait for an instance to become ready (useful after a guest reboot)",
		Long: `Re-polls the provider's readiness check (Parallels Tools / TCP:22 / etc).
Returns when the instance is ready or fails on ready.timeout.

Typical use: after a flow step that reboots the guest, call vmlab wait before
the next vmlab with so subsequent commands don't race the reboot.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pr, inst, err := resolveInstance(args[0])
			if err != nil {
				return err
			}
			waiter, ok := pr.(provider.ReadyWaiter)
			if !ok {
				return fmt.Errorf("provider %q does not support waiting", inst.Provider)
			}
			if err := waiter.WaitReady(cmd.Context(), inst); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ready: %s\n", inst.Name)
			return nil
		},
	}
	return c
}
