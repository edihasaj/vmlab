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
	AllowWrite bool
	// In/Out override stdio (for tests). When nil, os.Stdin/os.Stdout are used.
	In  io.Reader
	Out io.Writer
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
	registerTools(s, opts.AllowWrite)

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
