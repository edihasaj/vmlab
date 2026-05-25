// Package mcp serves vmlab tools over Model Context Protocol using the
// mark3labs/mcp-go SDK.
package mcp

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/edihasaj/vmlab/internal/version"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Options configures the server.
type Options struct {
	// AllowWrite gates tools that mutate target state (run/shell/gui/web).
	// True is shorthand for "register every write tool"; for per-tool
	// granularity prefer AllowedTools instead.
	AllowWrite bool
	// AllowedTools, when non-empty, registers exactly the named write tools
	// (the read-only tools remain available unconditionally). Takes
	// precedence over AllowWrite when set — use one or the other.
	//
	// Example: AllowedTools=["vmlab_run","vmlab_matrix_run"] gives an agent
	// the ability to execute flows but not lifecycle (vmlab_up/down/with)
	// or orphan destruction.
	AllowedTools []string
	// In/Out override stdio (for tests). When nil, os.Stdin/os.Stdout are used.
	In  io.Reader
	Out io.Writer
}

// allows reports whether a named write tool should be registered under
// the current Options. Reads (vmlab_targets, vmlab_doctor, …) are always
// allowed and skip this check.
func (o Options) allows(toolName string) bool {
	if len(o.AllowedTools) > 0 {
		for _, n := range o.AllowedTools {
			if n == toolName {
				return true
			}
		}
		return false
	}
	return o.AllowWrite
}

// Serve runs the MCP server until ctx is cancelled or stdin closes.
func Serve(ctx context.Context, opts Options) error {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	s := mcpserver.NewMCPServer(
		"vmlab",
		version.Version,
		mcpserver.WithToolCapabilities(true),
	)
	registerTools(s, opts)
	registerExtraTools(s, opts)

	stdio := mcpserver.NewStdioServer(s)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))
	return stdio.Listen(ctx, opts.In, opts.Out)
}

// helperResult turns plain text into an MCP CallToolResult.
func helperResult(text string) *mcpgo.CallToolResult {
	return mcpgo.NewToolResultText(text)
}

func helperError(text string) *mcpgo.CallToolResult {
	return mcpgo.NewToolResultError(text)
}
