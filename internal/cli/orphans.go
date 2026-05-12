package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newOrphansCmd() *cobra.Command {
	var (
		asJSON  bool
		destroy bool
	)
	c := &cobra.Command{
		Use:   "orphans",
		Short: "List (and optionally clean) cloud resources tagged vmlab=*",
		Long: `Cost safety net. Scans configured cloud providers for resources that carry a
"vmlab=<run-id>" label — these are servers vmlab created but never disposed
of (typically because a crash skipped the cleanup hook). With --destroy the
matching resources are deleted; otherwise just listed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			orphans, err := scanHetznerOrphans(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(orphans)
			}
			if len(orphans) == 0 {
				fmt.Fprintln(out, "no vmlab-tagged resources found")
				return nil
			}
			fmt.Fprintf(out, "%-12s %-20s %-15s %s\n", "PROVIDER", "NAME", "STATUS", "LABEL")
			for _, o := range orphans {
				fmt.Fprintf(out, "%-12s %-20s %-15s %s\n", o.Provider, o.Name, o.Status, o.Label)
			}
			if !destroy {
				return nil
			}
			for _, o := range orphans {
				if err := deleteOrphan(cmd.Context(), o); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "delete %s/%s: %v\n", o.Provider, o.Name, err)
					continue
				}
				fmt.Fprintf(out, "destroyed %s/%s\n", o.Provider, o.Name)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	c.Flags().BoolVar(&destroy, "destroy", false, "destroy listed resources (cost-saving sweep)")
	return c
}

// Orphan is one stranded cloud resource.
type Orphan struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Label    string `json:"label"`
}

func scanHetznerOrphans(ctx context.Context) ([]Orphan, error) {
	if _, err := exec.LookPath("hcloud"); err != nil {
		// no hcloud → no hetzner orphans to scan; not an error.
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "hcloud", "server", "list", "-l", "vmlab", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hcloud server list: %w", err)
	}
	var servers []struct {
		Name   string            `json:"name"`
		Status string            `json:"status"`
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(out, &servers); err != nil {
		return nil, fmt.Errorf("parse hcloud server list: %w", err)
	}
	orphans := make([]Orphan, 0, len(servers))
	for _, s := range servers {
		label := s.Labels["vmlab"]
		if label == "" {
			continue
		}
		orphans = append(orphans, Orphan{
			Provider: "hetzner",
			Name:     s.Name,
			Status:   s.Status,
			Label:    "vmlab=" + label,
		})
	}
	return orphans, nil
}

func deleteOrphan(ctx context.Context, o Orphan) error {
	switch o.Provider {
	case "hetzner":
		c := exec.CommandContext(ctx, "hcloud", "server", "delete", o.Name)
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return fmt.Errorf("unknown provider: %s", o.Provider)
}
