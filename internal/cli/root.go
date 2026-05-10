// Package cli wires up the cobra command tree for vmlab.
package cli

import (
	"github.com/edihasaj/vmlab/internal/version"
	"github.com/spf13/cobra"
)

// NewRoot returns the top-level cobra command with all subcommands attached.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "vmlab",
		Short:         "One CLI to install, set up, test, and verify software across any reachable target.",
		Long:          "vmlab is a transport-agnostic orchestrator for cross-platform verify loops. It does not replace crabbox / abx / guiport / adb / idb / Maestro — it composes them.",
		SilenceUsage:  true,
		SilenceErrors: false,
		Version:       version.Version,
	}
	root.SetVersionTemplate("vmlab {{.Version}}\n")

	root.AddCommand(
		newInitCmd(),
		newVersionCmd(),
		newTargetCmd(),
		newDoctorCmd(),
		newRunCmd(),
		newShellCmd(),
		newWebCmd(),
		newGUICmd(),
		newScreenshotCmd(),
		newEvidenceCmd(),
		newServeCmd(),
	)
	return root
}
