package gcp

import (
	"context"
	"testing"
)

func TestHourlyUSDHonoursOverride(t *testing.T) {
	inst := instance("demo", map[string]any{"hourlyUSD": 0.04})
	_, rate, err := New().HourlyUSD(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.04 {
		t.Errorf("expected override 0.04, got %v", rate)
	}
}

func TestHourlyUSDNoOverrideReturnsZero(t *testing.T) {
	_, rate, err := New().HourlyUSD(context.Background(), instance("demo", nil))
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0 {
		t.Errorf("expected 0 when no override is set, got %v", rate)
	}
}
