package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	for _, want := range []string{"vmlab_targets", "vmlab_doctor", "vmlab_evidence", "vmlab_instances",
		"vmlab_usage", "vmlab_orphans"} {
		if !got[want] {
			t.Errorf("missing tool %q (have: %v)", want, names)
		}
	}
	for _, no := range []string{"vmlab_run", "vmlab_web", "vmlab_gui", "vmlab_up", "vmlab_down", "vmlab_with",
		"vmlab_fleet_run", "vmlab_image_build", "vmlab_notify_test", "vmlab_cancel", "vmlab_orphans_destroy",
		"vmlab_grant"} {
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
	for _, want := range []string{"vmlab_run", "vmlab_web", "vmlab_gui", "vmlab_up", "vmlab_down", "vmlab_with",
		"vmlab_fleet_run", "vmlab_image_build", "vmlab_notify_test", "vmlab_cancel", "vmlab_orphans_destroy",
		"vmlab_grant"} {
		if !got[want] {
			t.Errorf("missing tool %q (have: %v)", want, names)
		}
	}
}

// setupParallelsHome wires a fake $HOME with a single parallels instance and
// a stubbed `prlctl` on PATH. The stub body decides the response.
func setupParallelsHome(t *testing.T, stubBody string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	instDir := filepath.Join(home, ".vmlab", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`name: smoke
provider: parallels
parallels:
  vm: smoke
ready:
  kind: parallels-tools
  timeout: 2s
disposition:
  on_success: suspend
  only_if_we_started: true
`)
	if err := os.WriteFile(filepath.Join(instDir, "smoke.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	stubDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
%s
`, filepath.Join(stubDir, "prlctl.args"), stubBody)
	if err := os.WriteFile(filepath.Join(stubDir, "prlctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func toolText(t *testing.T, resp map[string]any) string {
	t.Helper()
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %#v", resp)
	}
	content, _ := r["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("empty content: %#v", r)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func TestMCPInstancesListsConfigured(t *testing.T) {
	setupParallelsHome(t, `exit 0`)
	c := startServer(t, false)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_instances",
		"arguments": map[string]any{},
	}, 10)
	text := toolText(t, resp)
	if !strings.Contains(text, "smoke") || !strings.Contains(text, "parallels") {
		t.Errorf("expected smoke/parallels in instances output, got: %s", text)
	}
}

func TestMCPUpIdempotent(t *testing.T) {
	setupParallelsHome(t, `case "$1" in
status) echo 'VM "smoke" exist running'; exit 0 ;;
exec) exit 0 ;;
esac`)
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_up",
		"arguments": map[string]any{"instance": "smoke"},
	}, 11)
	text := toolText(t, resp)
	if !strings.Contains(text, `"changed": false`) {
		t.Errorf("expected changed=false when already running, got: %s", text)
	}
}

func TestMCPUpStartsSuspended(t *testing.T) {
	setupParallelsHome(t, `STATE="$HOME/bin/started"
case "$1" in
status)
  if [ -f "$STATE" ]; then echo 'VM "smoke" exist running'; else echo 'VM "smoke" exist suspended'; fi
  exit 0 ;;
start) touch "$STATE"; exit 0 ;;
exec) exit 0 ;;
esac`)
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_up",
		"arguments": map[string]any{"instance": "smoke"},
	}, 12)
	text := toolText(t, resp)
	if !strings.Contains(text, `"changed": true`) {
		t.Errorf("expected changed=true when starting from suspended, got: %s", text)
	}
}

func TestMCPDownRejectsUnknown(t *testing.T) {
	setupParallelsHome(t, `exit 0`)
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_down",
		"arguments": map[string]any{"instance": "nope"},
	}, 13)
	r, _ := resp["result"].(map[string]any)
	if r == nil {
		t.Fatalf("no result: %#v", resp)
	}
	if isErr, _ := r["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for unknown instance, got: %#v", r)
	}
}

func TestMCPWithRequiresCommand(t *testing.T) {
	setupParallelsHome(t, `exit 0`)
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_with",
		"arguments": map[string]any{"instance": "smoke"},
	}, 14)
	r, _ := resp["result"].(map[string]any)
	if r == nil {
		t.Fatalf("no result: %#v", resp)
	}
	if isErr, _ := r["isError"].(bool); !isErr {
		t.Errorf("expected isError=true when command is missing, got: %#v", r)
	}
}

func TestAllowedToolsRegistersOnlyListed(t *testing.T) {
	// Spin a server with AllowedTools=["vmlab_run"]. Confirm vmlab_run is
	// listed but vmlab_orphans_destroy (also a write tool) is not.
	clientToServer, serverIn := io.Pipe()
	serverOut, clientFromServer := io.Pipe()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = Serve(ctx, Options{
			In:           clientToServer,
			Out:          clientFromServer,
			AllowedTools: []string{"vmlab_run"},
		})
	}()
	c := &rpcClient{in: serverIn, out: bufio.NewReader(serverOut)}
	t.Cleanup(func() {
		_ = serverIn.Close()
		_ = clientFromServer.Close()
	})
	c.initialize(t)
	resp := c.call(t, "tools/list", map[string]any{}, 99)
	names := toolNames(resp)
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["vmlab_run"] {
		t.Errorf("vmlab_run missing from AllowedTools=[vmlab_run]: %v", names)
	}
	if got["vmlab_orphans_destroy"] {
		t.Errorf("vmlab_orphans_destroy registered without explicit allow: %v", names)
	}
	if got["vmlab_up"] {
		t.Errorf("vmlab_up registered without explicit allow: %v", names)
	}
}

func TestInferNeedsGrant(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		errMsg    string
		want      []string
	}{
		{
			name:      "guiport accessibility not trusted",
			transport: "guiport",
			errMsg:    "Accessibility permission not trusted for guiport",
			want:      []string{"accessibility"},
		},
		{
			name:      "screen recording not granted",
			transport: "guiport",
			errMsg:    "screen recording not granted; grant via System Settings",
			want:      []string{"screen-recording"},
		},
		{
			name:      "non-guiport transport returns nil",
			transport: "ssh",
			errMsg:    "Accessibility not trusted",
			want:      nil,
		},
		{
			name:      "unrelated error",
			transport: "guiport",
			errMsg:    "element not found",
			want:      nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inferNeedsGrant(tc.transport, fmt.Errorf("%s", tc.errMsg))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
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

func TestMCPUsageAggregatesEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	runsDir := filepath.Join(home, ".vmlab", "runs")
	writeMCPRun(t, runsDir, "r1", "hetzner", "linux-a", 0, 1000, 5000, 200)
	writeMCPRun(t, runsDir, "r2", "azure", "win-b", 1, 1500, 800, 300)

	c := startServer(t, false)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_usage",
		"arguments": map[string]any{},
	}, 50)
	text := toolText(t, resp)
	if !strings.Contains(text, "linux-a") || !strings.Contains(text, "win-b") {
		t.Errorf("usage missing instances: %s", text)
	}
}

func TestMCPCancelMissingRunFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_cancel",
		"arguments": map[string]any{"runId": "no-such-run"},
	}, 51)
	r, _ := resp["result"].(map[string]any)
	if r == nil {
		t.Fatalf("no result: %#v", resp)
	}
	if isErr, _ := r["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for missing run, got: %#v", r)
	}
}

func TestMCPCancelRequiresRunId(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := startServer(t, true)
	c.initialize(t)
	resp := c.call(t, "tools/call", map[string]any{
		"name":      "vmlab_cancel",
		"arguments": map[string]any{},
	}, 52)
	r, _ := resp["result"].(map[string]any)
	if isErr, _ := r["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for missing runId, got: %#v", r)
	}
}

func writeMCPRun(t *testing.T, root, id, provider, instance string, exit int, upMs, runMs, downMs int64) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{
		"id":         id,
		"startedAt":  time.Now().UTC().Format(time.RFC3339Nano),
		"exitCode":   exit,
		"durationMs": upMs + runMs + downMs,
		"lifecycle": map[string]any{
			"instance": instance,
			"provider": provider,
			"upMs":     upMs,
			"runMs":    runMs,
			"downMs":   downMs,
		},
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
