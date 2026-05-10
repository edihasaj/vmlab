// Package transport defines the abstract surface every adapter implements.
package transport

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/edihasaj/vmlab/internal/target"
)

// Result captures the outcome of a Run call.
type Result struct {
	ExitCode int
	Duration int64 // milliseconds
}

// Health reports the status of a transport for a target.
type Health struct {
	OK      bool
	Message string
	Details map[string]string
}

// GUIAction is a structured request for a GUI transport (guiport, etc).
type GUIAction struct {
	Kind     string         `json:"kind"` // click, type, screenshot, run-flow
	Selector string         `json:"selector,omitempty"`
	Text     string         `json:"text,omitempty"`
	Path     string         `json:"path,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// Caps mirrors target.Caps so transports can advertise built-in capabilities.
type Caps = target.Caps

// Transport is the unified interface every adapter implements.
type Transport interface {
	Name() string
	Capabilities() Caps
	Doctor(ctx context.Context, t target.Target) Health
	Sync(ctx context.Context, t target.Target, src string) error
	Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error)
	Shell(ctx context.Context, t target.Target) error
	Screenshot(ctx context.Context, t target.Target, path string) error
	GUI(ctx context.Context, t target.Target, action GUIAction) error
}

// Registry maps transport name -> implementation.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Transport
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[string]Transport{}} }

// Register a transport. Panics on duplicate registration.
func (r *Registry) Register(t Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[t.Name()]; ok {
		panic("transport already registered: " + t.Name())
	}
	r.m[t.Name()] = t
}

// Get fetches a transport by name.
func (r *Registry) Get(name string) (Transport, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.m[name]
	if !ok {
		return nil, fmt.Errorf("unknown transport: %q", name)
	}
	return t, nil
}

// Names returns registered transport names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	return out
}

// All returns every registered transport.
func (r *Registry) All() []Transport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Transport, 0, len(r.m))
	for _, v := range r.m {
		out = append(out, v)
	}
	return out
}

// Default returns a registry pre-populated with all built-in transports.
func Default() *Registry {
	r := NewRegistry()
	r.Register(NewCrabbox())
	r.Register(NewABX())
	r.Register(NewGuiport())
	r.Register(NewADB())
	r.Register(NewIDB())
	r.Register(NewSimctl())
	r.Register(NewMaestro())
	r.Register(NewLocal())
	return r
}
