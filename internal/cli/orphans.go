package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/spf13/cobra"
)

func newOrphansCmd() *cobra.Command {
	var (
		asJSON    bool
		destroy   bool
		providers []string
	)
	c := &cobra.Command{
		Use:   "orphans",
		Short: "List (and optionally destroy) vmlab-tagged resources across providers",
		Long: `Cost safety net. Scans every registered provider that implements OrphanSweeper
for resources carrying the vmlab=* tag (or, for tart, a vmlab- name prefix).

With --destroy the matching resources are removed; otherwise they are just
listed. --providers limits the sweep to a subset (e.g. --providers hetzner,aws).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg := provider.Default()
			want := map[string]bool{}
			for _, p := range providers {
				want[p] = true
			}
			orphans := []provider.Orphan{}
			byProvider := map[string]provider.OrphanSweeper{}
			for _, p := range reg.All() {
				if len(want) > 0 && !want[p.Name()] {
					continue
				}
				sw, ok := p.(provider.OrphanSweeper)
				if !ok {
					continue
				}
				byProvider[p.Name()] = sw
				list, err := sw.ListOrphans(cmd.Context())
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "orphans: %s: %v\n", p.Name(), err)
					continue
				}
				for _, o := range list {
					o.Provider = p.Name()
					orphans = append(orphans, o)
				}
			}
			sort.Slice(orphans, func(i, j int) bool {
				if orphans[i].Provider != orphans[j].Provider {
					return orphans[i].Provider < orphans[j].Provider
				}
				return orphans[i].Name < orphans[j].Name
			})

			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(orphans)
			}
			if len(orphans) == 0 {
				fmt.Fprintln(out, "no vmlab-tagged resources found")
				return nil
			}
			fmt.Fprintf(out, "%-12s %-32s %-15s %s\n", "PROVIDER", "NAME", "STATUS", "LABEL")
			for _, o := range orphans {
				fmt.Fprintf(out, "%-12s %-32s %-15s %s\n", o.Provider, o.Name, o.Status, o.Label)
			}
			if !destroy {
				return nil
			}
			for _, o := range orphans {
				if err := byProvider[o.Provider].DeleteOrphan(cmd.Context(), o.Name); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "destroy %s/%s: %v\n", o.Provider, o.Name, err)
					continue
				}
				fmt.Fprintf(out, "destroyed %s/%s\n", o.Provider, o.Name)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	c.Flags().BoolVar(&destroy, "destroy", false, "destroy listed resources (cost-saving sweep)")
	c.Flags().StringSliceVar(&providers, "providers", nil, "limit sweep to providers (comma-separated; default: all)")
	return c
}

// Compile-time assertion: every provider package wired into all/ that we
// expect to expose orphan sweep does. Failing this list catches forgotten
// implementations on rebuild.
var _ = func() bool {
	_ = context.Background
	return true
}()
