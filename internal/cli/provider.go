package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/spf13/cobra"
)

func newProviderCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "provider",
		Short: "Manage VM/instance providers",
	}
	c.AddCommand(providerLsCmd(), providerDoctorCmd())
	return c
}

func providerLsCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registered providers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg := provider.Default()
			names := reg.Names()
			sort.Strings(names)
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{"providers": names})
			}
			if len(names) == 0 {
				fmt.Fprintln(out, "no providers registered yet (P2+ adds parallels, hetzner)")
				return nil
			}
			fmt.Fprintln(out, "PROVIDER")
			for _, n := range names {
				fmt.Fprintln(out, n)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func providerDoctorCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Health-check every registered provider",
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg := provider.Default()
			out := cmd.OutOrStdout()
			if len(reg.Names()) == 0 {
				fmt.Fprintln(out, "no providers registered yet")
				return nil
			}
			// providers operate per-instance; doctor without an instance is a
			// presence check only.
			fmt.Fprintf(out, "%-12s %s\n", "PROVIDER", "STATUS")
			for _, p := range reg.All() {
				fmt.Fprintf(out, "%-12s %s\n", p.Name(), "registered")
			}
			return nil
		},
	}
	return c
}
