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
	c.AddCommand(providerLsCmd(), providerDoctorCmd(), providerValidateCmd())
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

// providerValidateCmd dry-runs a read-only credential check against a single
// provider. Useful before scheduling a long flow against a cloud target that
// requires a token: catch HCLOUD_TOKEN missing / scoped wrong / expired
// before any state-mutating call.
func providerValidateCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "validate <provider>",
		Short: "Dry-run credentials against a provider (no mutations)",
		Long: `Calls the provider's cheapest read-only API endpoint to confirm
credentials work. Hetzner: ` + "`hcloud server-type list -o noheader`. " + `
Returns 0 on success; non-zero with the provider's own diagnostic otherwise.

Only providers that implement the Validator interface support this — the
others print a clear "not implemented" line and exit 0.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			reg := provider.Default()
			pr, err := reg.Get(name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			v, ok := pr.(provider.Validator)
			if !ok {
				if asJSON {
					return json.NewEncoder(out).Encode(map[string]any{
						"provider":  name,
						"supported": false,
					})
				}
				fmt.Fprintf(out, "%s: validate not implemented for this provider\n", name)
				return nil
			}
			if err := v.Validate(cmd.Context()); err != nil {
				if asJSON {
					_ = json.NewEncoder(out).Encode(map[string]any{
						"provider": name,
						"ok":       false,
						"error":    err.Error(),
					})
					return err
				}
				return fmt.Errorf("%s: %w", name, err)
			}
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"provider": name,
					"ok":       true,
				})
			}
			fmt.Fprintf(out, "✓ %s credentials valid\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
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
