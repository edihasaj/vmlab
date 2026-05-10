package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// roundtrip drives the server with a request and returns the parsed response.
func roundtrip(t *testing.T, req string, allowWrite bool) map[string]any {
	t.Helper()
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Serve(ctx, Options{In: in, Out: &out, AllowWrite: allowWrite}); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("parse: %v - raw=%s", err, out.String())
	}
	return resp
}

func TestInitialize(t *testing.T) {
	resp := roundtrip(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, false)
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %#v", resp)
	}
	if r["protocolVersion"] == "" {
		t.Fatalf("missing protocolVersion")
	}
}

func TestToolsListReadOnly(t *testing.T) {
	resp := roundtrip(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, false)
	r := resp["result"].(map[string]any)
	tools := r["tools"].([]any)
	names := map[string]bool{}
	for _, t := range tools {
		names[t.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"vmlab_targets", "vmlab_doctor", "vmlab_evidence"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	for _, no := range []string{"vmlab_run", "vmlab_web", "vmlab_gui"} {
		if names[no] {
			t.Errorf("write-mode tool %q unexpectedly exposed without --allow-write", no)
		}
	}
}

func TestToolsListAllowWrite(t *testing.T) {
	resp := roundtrip(t, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, true)
	r := resp["result"].(map[string]any)
	tools := r["tools"].([]any)
	names := map[string]bool{}
	for _, t := range tools {
		names[t.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"vmlab_run", "vmlab_web", "vmlab_gui"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestUnknownMethod(t *testing.T) {
	resp := roundtrip(t, `{"jsonrpc":"2.0","id":4,"method":"nope"}`, false)
	if resp["error"] == nil {
		t.Fatalf("expected error: %#v", resp)
	}
}
