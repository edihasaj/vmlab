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
// Real providers are wired in later phases; for now it's empty so the CLI
// surface compiles and `vmlab provider ls` reports zero providers.
func Default() *Registry {
	return NewRegistry()
}
