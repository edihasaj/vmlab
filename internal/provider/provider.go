// Package provider defines the lifecycle abstraction that sits on top of the
// transport layer. A Provider owns power-state for an Instance (idempotent
// Status / Up / Down) and emits a Target the existing transports can consume.
package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/edihasaj/vmlab/internal/target"
)

// State is the coarse lifecycle state of an instance.
type State int

const (
	StateUnknown State = iota
	StateNotFound
	StateStopped
	StateSuspended
	StateStarting
	StateRunning
	StateReady
)

// String returns the canonical lowercase name of the state.
func (s State) String() string {
	switch s {
	case StateNotFound:
		return "not-found"
	case StateStopped:
		return "stopped"
	case StateSuspended:
		return "suspended"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateReady:
		return "ready"
	default:
		return "unknown"
	}
}

// Dispose controls how Down releases an instance.
type Dispose int

const (
	DisposeKeep Dispose = iota
	DisposeSuspend
	DisposePowerOff
	DisposeDestroy
)

// String returns the canonical lowercase name of the disposition.
func (d Dispose) String() string {
	switch d {
	case DisposeSuspend:
		return "suspend"
	case DisposePowerOff:
		return "poweroff"
	case DisposeDestroy:
		return "destroy"
	default:
		return "keep"
	}
}

// ParseDispose accepts the canonical form ("keep", "suspend", "poweroff",
// "destroy") and returns the matching Dispose.
func ParseDispose(s string) (Dispose, error) {
	switch s {
	case "", "keep":
		return DisposeKeep, nil
	case "suspend":
		return DisposeSuspend, nil
	case "poweroff", "stop":
		return DisposePowerOff, nil
	case "destroy":
		return DisposeDestroy, nil
	}
	return DisposeKeep, fmt.Errorf("unknown dispose %q", s)
}

// Health mirrors transport.Health so Doctor can be reported uniformly.
type Health struct {
	OK      bool              `json:"ok"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// EnsureResult records what Up actually did. Cleanup respects Changed so we
// never suspend an instance the user was already using.
type EnsureResult struct {
	Changed    bool   `json:"changed"`
	PriorState State  `json:"priorState"`
	Reason     string `json:"reason,omitempty"`
}

// Provider is the lifecycle surface every backend implements.
type Provider interface {
	Name() string
	Doctor(ctx context.Context, i Instance) Health
	Status(ctx context.Context, i Instance) (State, error)
	Up(ctx context.Context, i Instance) (target.Target, EnsureResult, error)
	Down(ctx context.Context, i Instance, d Dispose) error
}

// Priced is an optional provider capability for backends that know their
// per-instance hourly rate. EnforceBudget consults this before Up so a
// misconfigured instance type can't quietly burn money.
//
// Implementations should return ("", 0, nil) when the rate genuinely
// can't be derived (custom-priced contracts, unknown region, etc.) —
// EnforceBudget treats the missing rate as "skip the check" rather than
// failing closed, so a half-known price table doesn't block every Up.
type Priced interface {
	HourlyUSD(ctx context.Context, i Instance) (currency string, rate float64, err error)
}

// ErrBudgetExceeded is returned by EnforceBudget when the provider's
// resolved hourly rate is above the instance's budget.hourlyUSD cap.
type ErrBudgetExceeded struct {
	Instance  string
	CapUSD    float64
	ActualUSD float64
}

func (e ErrBudgetExceeded) Error() string {
	return fmt.Sprintf("budget: instance %q rate $%.4f/hr exceeds cap $%.4f/hr", e.Instance, e.ActualUSD, e.CapUSD)
}

// Validator is an optional capability for providers that can dry-run a
// read-only API call to confirm credentials work without firing any
// mutations. Used by `vmlab provider validate <name>`.
//
// Implementations should pick the cheapest read endpoint the provider
// offers (e.g. `hcloud server-type list`, `aws ec2 describe-regions`,
// `gcloud auth list`) so the check is fast and free.
type Validator interface {
	Validate(ctx context.Context) error
}

// UpEnforced is the wrapper every caller should use instead of p.Up directly.
// It runs EnforceBudget first, then calls Up. Centralising this keeps the
// budget check from being skipped on a new callsite — easier than ensuring
// 8 callers remember the guard.
func UpEnforced(ctx context.Context, p Provider, i Instance) (target.Target, EnsureResult, error) {
	if err := EnforceBudget(ctx, p, i); err != nil {
		return target.Target{}, EnsureResult{}, err
	}
	return p.Up(ctx, i)
}

// EnforceBudget checks the instance's budget.hourlyUSD against either the
// provider's own price quote (when it implements Priced) or the operator-
// provided override on the BudgetCfg itself. Returns nil when no cap is
// set or no rate is known. Providers should call this near the top of Up
// (after Status, before any state-mutating API call).
func EnforceBudget(ctx context.Context, p Provider, i Instance) error {
	if i.Budget.HourlyUSD <= 0 {
		return nil
	}
	rate := i.Budget.HourlyUSD // default: if Priced has no opinion, the operator's own number is the floor
	rate = 0
	if priced, ok := p.(Priced); ok {
		if _, r, err := priced.HourlyUSD(ctx, i); err == nil && r > 0 {
			rate = r
		}
	}
	if rate == 0 {
		// No provider-known rate; the budget cap acts as documentation
		// only. Don't fail — the operator already declared the intent.
		return nil
	}
	if rate > i.Budget.HourlyUSD {
		return ErrBudgetExceeded{Instance: i.Name, CapUSD: i.Budget.HourlyUSD, ActualUSD: rate}
	}
	return nil
}

// ReadyWaiter is an optional provider capability for re-polling guest
// readiness without doing a full Up cycle. Useful after a reboot mid-flow
// or before driving a fresh-booted box.
type ReadyWaiter interface {
	WaitReady(ctx context.Context, i Instance) error
}

// Restarter is an optional capability for providers that can reboot a running
// instance in place — and wait for it to come back ready — without a full
// down/up cycle. Used by `vmlab restart` to recover a guest whose agent has
// wedged (e.g. Parallels Tools returning PrlResult errors mid-run), which
// previously meant SSHing to the host and running `prlctl restart` by hand.
type Restarter interface {
	Restart(ctx context.Context, i Instance) error
}

// Snapshotter is an optional capability for providers that can checkpoint and
// restore VM state (Parallels, Hetzner via images, EC2 via AMIs, …). Callers
// detect support via a type assertion.
type Snapshotter interface {
	Snapshot(ctx context.Context, i Instance, name, description string) error
	Restore(ctx context.Context, i Instance, name string) error
	ListSnapshots(ctx context.Context, i Instance) ([]Snapshot, error)
	DeleteSnapshot(ctx context.Context, i Instance, name string) error
}

// Snapshot is one saved checkpoint surfaced via ListSnapshots.
type Snapshot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Date    string `json:"date,omitempty"`
	State   string `json:"state,omitempty"`
	Current bool   `json:"current,omitempty"`
	Parent  string `json:"parent,omitempty"`
}

// OrphanSweeper is an optional capability for providers that can enumerate
// and destroy resources they own which carry the vmlab=<name> tag — the
// cost safety net. Implemented per provider so the CLI can fan out across
// every registered backend.
type OrphanSweeper interface {
	ListOrphans(ctx context.Context) ([]Orphan, error)
	DeleteOrphan(ctx context.Context, name string) error
}

// Orphan is one stranded provider resource. Provider is filled by the CLI
// from the originating provider name; impls only need to populate Name,
// Status, Label.
type Orphan struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Label    string `json:"label"`
}

// Registry maps provider name -> implementation.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[string]Provider{}} }

// Register adds a provider. Panics on duplicate.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[p.Name()]; ok {
		panic("provider already registered: " + p.Name())
	}
	r.m[p.Name()] = p
}

// Get fetches a provider by name.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.m[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
	return p, nil
}

// Names returns registered provider names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	return out
}

// All returns every registered provider.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.m))
	for _, v := range r.m {
		out = append(out, v)
	}
	return out
}

// Default returns a registry pre-populated with built-in providers.
// Concrete providers are registered from internal/provider/<name>/init.go via
// SideEffectRegister so this package stays free of provider-specific imports.
func Default() *Registry {
	r := NewRegistry()
	for _, p := range builtin {
		r.Register(p)
	}
	return r
}

// builtin is the side-loaded list of providers. Concrete provider packages
// append to it via init().
var builtin []Provider

// SideEffectRegister adds a provider to the built-in set. Called from
// concrete provider package init() so Default() picks it up without an
// import cycle.
func SideEffectRegister(p Provider) {
	builtin = append(builtin, p)
}
