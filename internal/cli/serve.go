package cli

import (
	"github.com/edihasaj/vmlab/internal/mcp"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		mcpMode    bool
		allowWrite bool
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run vmlab as a server (MCP for agents)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !mcpMode {
				return cmd.Usage()
			}
			return mcp.Serve(cmd.Context(), mcp.Options{AllowWrite: allowWrite})
		},
	}
	c.Flags().BoolVar(&mcpMode, "mcp", false, "speak Model Context Protocol over stdio")
	c.Flags().BoolVar(&allowWrite, "allow-write", false, "allow tools that mutate (run/shell/gui); default is read-only")
	return c
}
