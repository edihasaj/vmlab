// vmlab crabbox — thin passthrough to the `crabbox` CLI. We only re-export the
// subcommands that vmlab users actually want to reach without leaving vmlab
// (checkpoint, warmup, image). Everything else stays on raw `crabbox`.
package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

const crabboxBinary = "crabbox"

func newCrabboxCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "crabbox",
		Short: "Passthrough to the crabbox CLI (checkpoint / warmup / image)",
		Long: "Forwards selected subcommands to the local `crabbox` binary so " +
			"vmlab users can reach broker-managed flows (provider-native " +
			"checkpoints, warm pools, AMI promote) without context-switching. " +
			"All flags and args after the subcommand are passed through verbatim.",
	}
	c.AddCommand(
		crabboxPassCmd("checkpoint", "Create / fork / list provider checkpoints (crabbox checkpoint ...)"),
		crabboxPassCmd("warmup", "Provision or claim a crabbox lease and wait until ready"),
		crabboxPassCmd("image", "AWS AMI bake/promote workflow (admin-token gated)"),
		crabboxPassCmd("pool", "Manage warm lease pools"),
	)
	return c
}

// crabboxPassCmd builds a cobra leaf that disables its own flag parsing and
// shells everything to `crabbox <sub> ...`. DisableFlagParsing keeps cobra from
// claiming flags like --id / --provider that belong to crabbox.
func crabboxPassCmd(sub, short string) *cobra.Command {
	return &cobra.Command{
		Use:                sub + " [args...]",
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCrabboxPassthrough(cmd, sub, args)
		},
	}
}

// crabboxRunner is overridden in tests to capture invocations without shelling
// to a real binary. Production path uses exec.Command + the parent's stdio.
var crabboxRunner = func(cmd *cobra.Command, args []string) error {
	if _, err := exec.LookPath(crabboxBinary); err != nil {
		return fmt.Errorf("crabbox not found on PATH (install from openclaw/crabbox)")
	}
	c := exec.CommandContext(cmd.Context(), crabboxBinary, args...)
	c.Stdin = os.Stdin
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("crabbox exited %d", ee.ExitCode())
		}
		return err
	}
	return nil
}

func runCrabboxPassthrough(cmd *cobra.Command, sub string, args []string) error {
	full := append([]string{sub}, args...)
	return crabboxRunner(cmd, full)
}
