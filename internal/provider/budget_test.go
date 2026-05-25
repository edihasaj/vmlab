package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

// stubPricedProvider is a minimal Provider that also satisfies Priced. We
// can't reuse a real cloud provider in unit tests since they shell out to
// the corresponding CLI binaries; the local stub keeps the budget logic
// honestly covered without that surface area.
type stubPricedProvider struct {
	rate float64
}

func (s stubPricedProvider) Name() string                            { return "stub" }
func (s stubPricedProvider) Doctor(context.Context, Instance) Health { return Health{OK: true} }
func (s stubPricedProvider) Status(context.Context, Instance) (State, error) {
	return StateRunning, nil
}
func (s stubPricedProvider) Up(context.Context, Instance) (target.Target, EnsureResult, error) {
	return target.Target{}, EnsureResult{}, nil
}
func (s stubPricedProvider) Down(context.Context, Instance, Dispose) error { return nil }
func (s stubPricedProvider) HourlyUSD(context.Context, Instance) (string, float64, error) {
	return "USD", s.rate, nil
}

func TestEnforceBudgetUnderCapAllows(t *testing.T) {
	p := stubPricedProvider{rate: 0.10}
	inst := Instance{Name: "demo", Budget: BudgetCfg{HourlyUSD: 1.00}}
	if err := EnforceBudget(context.Background(), p, inst); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestEnforceBudgetOverCapBlocks(t *testing.T) {
	p := stubPricedProvider{rate: 2.50}
	inst := Instance{Name: "expensive", Budget: BudgetCfg{HourlyUSD: 1.00}}
	err := EnforceBudget(context.Background(), p, inst)
	if err == nil {
		t.Fatal("expected ErrBudgetExceeded")
	}
	var be ErrBudgetExceeded
	if !errors.As(err, &be) {
		t.Fatalf("expected ErrBudgetExceeded, got %T: %v", err, err)
	}
	if be.CapUSD != 1.00 || be.ActualUSD != 2.50 {
		t.Errorf("wrong cap/actual: %+v", be)
	}
}

func TestEnforceBudgetNoCapSkips(t *testing.T) {
	p := stubPricedProvider{rate: 99.0}
	inst := Instance{Name: "uncapped"} // BudgetCfg zero-value
	if err := EnforceBudget(context.Background(), p, inst); err != nil {
		t.Fatalf("no-cap should pass, got %v", err)
	}
}

func TestEnforceBudgetUnpricedProviderSkips(t *testing.T) {
	// A provider that doesn't implement Priced should not block — the
	// budget acts as documentation only in that case.
	type unpricedProvider struct{ stubPricedProvider }
	// shadow HourlyUSD by using the zero-rate variant + no Priced interface
	// implementation: embed via composition but ensure the assertion fails.
	// Easiest: declare a fresh type with just Provider methods.
	type bare struct{ stubPricedProvider }
	// override Priced by simply not embedding it... actually embed and
	// the method is inherited. Use a distinct type instead:
	p := newBareProvider()
	inst := Instance{Name: "bare", Budget: BudgetCfg{HourlyUSD: 0.10}}
	if err := EnforceBudget(context.Background(), p, inst); err != nil {
		t.Fatalf("unpriced provider should skip, got %v", err)
	}
	_ = unpricedProvider{}
	_ = bare{}
}

// bareProvider implements Provider without Priced so the type-assert in
// EnforceBudget falls through and the check is skipped.
type bareProvider struct{}

func newBareProvider() Provider { return bareProvider{} }

func (bareProvider) Name() string                                    { return "bare" }
func (bareProvider) Doctor(context.Context, Instance) Health         { return Health{OK: true} }
func (bareProvider) Status(context.Context, Instance) (State, error) { return StateRunning, nil }
func (bareProvider) Up(context.Context, Instance) (target.Target, EnsureResult, error) {
	return target.Target{}, EnsureResult{}, nil
}
func (bareProvider) Down(context.Context, Instance, Dispose) error { return nil }
