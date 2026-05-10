package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/spf13/cobra"
)

func newEvidenceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "evidence",
		Short: "Manage evidence bundles",
	}
	c.AddCommand(evidenceListCmd(), evidenceShowCmd(), evidenceBundleCmd(), evidencePruneCmd())
	return c
}

func evidenceListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List recent runs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			runs, err := evidence.List(cfg.RunsDir)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(runs)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%-32s %-6s %-8s %s\n", "RUN-ID", "EXIT", "TARGETS", "STARTED")
			for _, r := range runs {
				ts := r.StartedAt.Local().Format(time.RFC3339)
				fmt.Fprintf(out, "%-32s %-6d %-8d %s\n", r.ID, r.ExitCode, len(r.Targets), ts)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func evidenceShowCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "show <run-id>",
		Short: "Print a run summary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			meta, err := evidence.Read(filepath.Join(cfg.RunsDir, args[0]))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(meta)
			}
			fmt.Fprintf(out, "id:        %s\n", meta.ID)
			fmt.Fprintf(out, "exit:      %d\n", meta.ExitCode)
			fmt.Fprintf(out, "duration:  %dms\n", meta.DurationMs)
			fmt.Fprintf(out, "selector:  %s\n", meta.Selector)
			if meta.Flow != "" {
				fmt.Fprintf(out, "flow:      %s\n", meta.Flow)
			}
			if meta.Cmd != "" {
				fmt.Fprintf(out, "cmd:       %s\n", meta.Cmd)
			}
			fmt.Fprintln(out, "targets:")
			for _, t := range meta.Targets {
				status := "ok"
				if t.ExitCode != 0 || t.Error != "" {
					status = "FAIL"
				}
				fmt.Fprintf(out, "  - %-20s %-10s exit=%d %s %s\n",
					t.Name, t.Transport, t.ExitCode, status,
					strings.TrimSpace(t.Error))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func evidenceBundleCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "bundle <run-id>",
		Short: "Zip a run directory for sharing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			runDir := filepath.Join(cfg.RunsDir, args[0])
			if out == "" {
				out = args[0] + ".zip"
			}
			if err := evidence.Bundle(runDir, out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
			return nil
		},
	}
	c.Flags().StringVarP(&out, "output", "o", "", "output zip path (default <run-id>.zip)")
	return c
}

func evidencePruneCmd() *cobra.Command {
	var olderThan time.Duration
	c := &cobra.Command{
		Use:   "prune",
		Short: "Delete runs older than --older-than (default uses config retention)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			d := olderThan
			if d == 0 {
				d = time.Duration(cfg.EvidenceRetentionDays) * 24 * time.Hour
			}
			if d <= 0 {
				return fmt.Errorf("retention must be > 0")
			}
			n, err := evidence.PruneOlderThan(cfg.RunsDir, time.Now().Add(-d))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %d run(s)\n", n)
			return nil
		},
	}
	c.Flags().DurationVar(&olderThan, "older-than", 0, "duration (e.g. 168h); default = retention from config")
	return c
}
