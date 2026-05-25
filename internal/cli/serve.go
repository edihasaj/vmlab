package cli

import (
	"github.com/edihasaj/vmlab/internal/mcp"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		mcpMode      bool
		allowWrite   bool
		allowedTools []string
	)
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run vmlab as a server (MCP for agents)",
		Long: `Start an MCP server over stdio for agent tooling.

Permissioning the write surface (mutually exclusive forms — pick one):

  --allow-write              shorthand for "register every write tool"
  --allow-tools name,name,…  register only the listed write tools

Read-only tools (vmlab_targets, vmlab_doctor, vmlab_evidence,
vmlab_instances, vmlab_usage, vmlab_orphans) are always registered.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !mcpMode {
				return cmd.Usage()
			}
			return mcp.Serve(cmd.Context(), mcp.Options{
				AllowWrite:   allowWrite,
				AllowedTools: allowedTools,
			})
		},
	}
	c.Flags().BoolVar(&mcpMode, "mcp", false, "speak Model Context Protocol over stdio")
	c.Flags().BoolVar(&allowWrite, "allow-write", false, "register every write tool (use --allow-tools for per-tool granularity)")
	c.Flags().StringSliceVar(&allowedTools, "allow-tools", nil, "register only these write tools, e.g. vmlab_run,vmlab_matrix_run")
	return c
}
