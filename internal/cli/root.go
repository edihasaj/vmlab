// Package cli wires up the cobra command tree for vmlab.
package cli

import (
	"github.com/edihasaj/vmlab/internal/logging"
	_ "github.com/edihasaj/vmlab/internal/provider/all"
	"github.com/edihasaj/vmlab/internal/version"
	"github.com/spf13/cobra"
)

// NewRoot returns the top-level cobra command with all subcommands attached.
func NewRoot() *cobra.Command {
	var verbose bool
	root := &cobra.Command{
		Use:           "vmlab",
		Short:         "One CLI to install, set up, test, and verify software across any reachable target.",
		Long:          "vmlab is a transport-agnostic orchestrator for cross-platform verify loops. It does not replace crabbox / abx / guiport / adb / idb / Maestro — it composes them.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			logging.Setup(verbose, cmd.ErrOrStderr())
		},
	}
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	root.SetVersionTemplate("vmlab {{.Version}}\n")

	root.AddCommand(
		newInitCmd(),
		newVersionCmd(),
		newTargetCmd(),
		newProviderCmd(),
		newInstanceCmd(),
		newDoctorCmd(),
		newRunCmd(),
		newUpCmd(),
		newDownCmd(),
		newWithCmd(),
		newSyncCmd(),
		newSnapshotCmd(),
		newWaitCmd(),
		newShellCmd(),
		newWebCmd(),
		newGUICmd(),
		newScreenshotCmd(),
		newEvidenceCmd(),
		newOrphansCmd(),
		newServeCmd(),
	)
	return root
}
