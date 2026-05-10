package schema

import (
	"strings"
	"testing"
)

func TestValidTarget(t *testing.T) {
	body := []byte(`
name: ubuntu
transport: crabbox
tags: [linux]
crabbox:
  configPath: ~/.crabbox/ubuntu.yaml
capabilities:
  shell: true
`)
	if err := ValidateTarget("ubuntu.yaml", body); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestUnknownTransport(t *testing.T) {
	body := []byte(`name: x
transport: telegraph
`)
	err := ValidateTarget("x.yaml", body)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "telegraph") && !strings.Contains(err.Error(), "transport") {
		t.Fatalf("expected mention of telegraph/transport, got: %v", err)
	}
}

func TestMissingTransport(t *testing.T) {
	body := []byte(`name: x`)
	if err := ValidateTarget("x.yaml", body); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidFlow(t *testing.T) {
	body := []byte(`
name: smoke
steps:
  - run: echo hi
  - assert: 'true'
`)
	if err := ValidateFlow("smoke.yaml", body); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestEmptyFlow(t *testing.T) {
	if err := ValidateFlow("empty.yaml", []byte(`name: foo
steps: []`)); err == nil {
		t.Fatal("expected error for empty steps")
	}
}

func TestBadStep(t *testing.T) {
	body := []byte(`steps:
  - name: bare
`)
	if err := ValidateFlow("bad.yaml", body); err == nil {
		t.Fatal("expected error: step needs run or assert")
	}
}
