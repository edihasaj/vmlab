package cli

import (
	"fmt"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

func newShellCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "shell <target>",
		Short: "Open an interactive shell on a target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := target.Load(p)
			if err != nil {
				return err
			}
			t, ok := r.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown target: %s", args[0])
			}
			tr, err := transport.Default().Get(t.Transport)
			if err != nil {
				return err
			}
			return tr.Shell(cmd.Context(), t)
		},
	}
	return c
}
