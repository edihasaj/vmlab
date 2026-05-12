package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/spf13/cobra"
)

const sampleRepoConfig = `# .vmlab.yaml — repo-level overrides for vmlab.
# Optional. User config lives in ~/.vmlab/config.yaml.
# evidenceRetentionDays: 30
# defaultMaxParallel: 4
`

const sampleFlow = `# flows/install.yaml — minimal flow.
# Each step is shell-only by design; push complexity into your scripts.
name: install
steps:
  - run: echo "hello from vmlab"
  - assert: test -e ./README.md || echo "no README"
`

func newInitCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Initialise vmlab dirs and write a starter .vmlab.yaml in the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(p); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "user dir:      %s\n", p.UserDir)
			fmt.Fprintf(out, "targets dir:   %s\n", p.TargetDir[0])
			fmt.Fprintf(out, "instances dir: %s\n", p.InstanceDir[0])
			fmt.Fprintf(out, "runs dir:      %s\n", p.RunsDir)

			repoCfg := filepath.Join(p.RepoDir, "..", ".vmlab.yaml")
			repoCfg = filepath.Clean(repoCfg)
			if _, err := os.Stat(repoCfg); err == nil && !force {
				fmt.Fprintf(out, "skip: %s exists (use --force to overwrite)\n", repoCfg)
			} else {
				if err := os.WriteFile(repoCfg, []byte(sampleRepoConfig), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "wrote: %s\n", repoCfg)
			}

			flowsDir := filepath.Join(p.RepoDir, "..", "flows")
			flowsDir = filepath.Clean(flowsDir)
			if err := os.MkdirAll(flowsDir, 0o755); err != nil {
				return err
			}
			sampleFlowPath := filepath.Join(flowsDir, "install.yaml")
			if _, err := os.Stat(sampleFlowPath); err == nil && !force {
				fmt.Fprintf(out, "skip: %s exists\n", sampleFlowPath)
			} else {
				if err := os.WriteFile(sampleFlowPath, []byte(sampleFlow), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "wrote: %s\n", sampleFlowPath)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return c
}
