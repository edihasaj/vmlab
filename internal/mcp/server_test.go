package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// rpcClient is a tiny stdio JSON-RPC harness used to exercise the SDK-backed
// server with real protocol traffic.
type rpcClient struct {
	in  *io.PipeWriter
	out *bufio.Reader
	wg  *sync.WaitGroup
}

func startServer(t *testing.T, allowWrite bool) *rpcClient {
	t.Helper()
	clientToServer, serverIn := io.Pipe()
	serverOut, clientFromServer := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = Serve(ctx, Options{In: clientToServer, Out: clientFromServer, AllowWrite: allowWrite})
	}()
	t.Cleanup(func() {
		_ = serverIn.Close()
		_ = clientFromServer.Close()
		wg.Wait()
	})
	return &rpcClient{
		in:  serverIn,
		out: bufio.NewReader(serverOut),
		wg:  &wg,
	}
}

func (c *rpcClient) call(t *testing.T, method string, params any, id int) map[string]any {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if _, err := c.in.Write(b); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := c.out.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v (raw=%q)", err, string(line))
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, string(line))
	}
	return resp
}

func (c *rpcClient) initialize(t *testing.T) {
	t.Helper()
	c.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}, 0)
	// initialized notification (no id, no response)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	c.in.Write(append(body, '\n'))
}

func toolNames(resp map[string]any) []string {
	r, _ := resp["result"].(map[string]any)
	tools, _ := r["tools"].([]any)
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

func TestInitialize(t *testing.T) {
	c := startServer(t, false)
	resp := c.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}, 1)
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %#v", resp)
	}
	if r["protocolVersion"] == "" || r["protocolVersion"] == nil {
		t.Fatalf("missing protocolVersion: %#v", r)
	}
}

func TestToolsListReadOnly(t *testing.T) {
	c := startServer(t, false)
	c.initialize(t)
	resp := c.call(t, "tools/list", map[string]any{}, 2)
	names := toolNames(resp)
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for _, want := range []string{"vmlab_targets", "vmlab_doctor", "vmlab_evidence"} {
		if !got[want] {
			t.Errorf("missing tool %q (have: %v)", want, names)
		}
	}
	for _, no := range []string{"vmlab_run", "vmlab_web", "vmlab_gui"} {
		if got[no] {
			t.Errorf("write-mode tool %q unexpectedly exposed without --allow-write", no)
		}
	}
}

func TestToolsListAllowWrite(t *testing.T) {
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/list", map[string]any{}, 2)
	names := toolNames(resp)
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for _, want := range []string{"vmlab_run", "vmlab_web", "vmlab_gui"} {
		if !got[want] {
			t.Errorf("missing tool %q (have: %v)", want, names)
		}
	}
}

func TestToolsCallTargetsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := startServer(t, false)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_targets",
		"arguments": map[string]any{},
	}, 3)
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %#v", resp)
	}
	content, _ := r["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "null") && !strings.Contains(text, "[]") {
		t.Fatalf("expected empty targets, got: %s", text)
	}
}
