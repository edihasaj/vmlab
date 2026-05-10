// Package mcp implements a minimal Model Context Protocol server over stdio.
//
// The wire format is JSON-RPC 2.0 with newline-delimited messages. We support a
// pragmatic subset: `initialize`, `tools/list`, `tools/call`, plus `ping`.
// This is a small surface deliberately — it covers what Claude Code needs to
// drive vmlab end-to-end, with no external dependency on a fast-moving SDK.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Options configures the server.
type Options struct {
	// AllowWrite gates tools that mutate target state (run/shell/gui/web).
	AllowWrite bool
	// In/Out override stdio (for tests).
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
	s := &server{opts: opts, tools: defaultTools(opts.AllowWrite)}
	return s.run(ctx)
}

type server struct {
	opts  Options
	tools []tool
	mu    sync.Mutex // serialises writes
}

// JSON-RPC 2.0 envelopes.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *server) run(ctx context.Context) error {
	br := bufio.NewReader(s.opts.In)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			s.handle(ctx, bytes.TrimSpace(line))
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *server) handle(ctx context.Context, raw []byte) {
	if len(raw) == 0 {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.respond(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "vmlab",
				"version": "dev",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}
	case "ping":
		resp.Result = map[string]any{"ok": true}
	case "tools/list":
		var out []map[string]any
		for _, t := range s.tools {
			out = append(out, map[string]any{
				"name":        t.name,
				"description": t.description,
				"inputSchema": t.inputSchema,
			})
		}
		resp.Result = map[string]any{"tools": out}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: err.Error()}
			break
		}
		t := s.findTool(p.Name)
		if t == nil {
			resp.Error = &rpcError{Code: -32601, Message: "unknown tool: " + p.Name}
			break
		}
		text, err := t.handler(ctx, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}
		} else {
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			}
		}
	default:
		// Notifications (no id) are silently ignored.
		if len(req.ID) == 0 {
			return
		}
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	if len(req.ID) == 0 {
		// Notifications get no response.
		return
	}
	s.respond(resp)
}

func (s *server) findTool(name string) *tool {
	for i := range s.tools {
		if s.tools[i].name == name {
			return &s.tools[i]
		}
	}
	return nil
}

func (s *server) respond(r rpcResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vmlab mcp marshal error:", err)
		return
	}
	b = append(b, '\n')
	_, _ = s.opts.Out.Write(b)
}
