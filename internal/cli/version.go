package cli

import (
	"encoding/json"
	"fmt"

	"github.com/edihasaj/vmlab/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "version",
		Short: "Print version info",
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"version": version.Version,
					"commit":  version.Commit,
					"date":    version.Date,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "vmlab %s (commit %s, built %s)\n",
				version.Version, version.Commit, version.Date)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}
