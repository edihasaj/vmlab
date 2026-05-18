package credresolver

import (
	"context"
	"testing"
)

func TestResolvePlainLiteral(t *testing.T) {
	got, err := Resolve(context.Background(), "https://example.com/hook")
	if err != nil {
		t.Fatalf("plain literal: %v", err)
	}
	if got != "https://example.com/hook" {
		t.Fatalf("plain literal not passed through: %q", got)
	}
}

func TestResolveEnvVar(t *testing.T) {
	t.Setenv("VMLAB_TEST_SECRET", "hunter2")
	got, err := Resolve(context.Background(), "env:VMLAB_TEST_SECRET")
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	if got != "hunter2" {
		t.Fatalf("env var not resolved: %q", got)
	}
}

func TestResolveEnvVarMissing(t *testing.T) {
	t.Setenv("VMLAB_TEST_SECRET", "")
	if _, err := Resolve(context.Background(), "env:VMLAB_TEST_SECRET"); err == nil {
		t.Fatalf("expected error for unset env var")
	}
}

func TestResolveEmptyString(t *testing.T) {
	got, err := Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("empty string: %v", err)
	}
	if got != "" {
		t.Fatalf("empty: %q", got)
	}
}
