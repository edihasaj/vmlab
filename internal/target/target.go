// Package target loads, validates, and selects targets.
package target

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/schema"
	"gopkg.in/yaml.v3"
)

// Caps describes optional capabilities a target supports.
type Caps struct {
	Shell      bool `yaml:"shell"`
	Sync       bool `yaml:"sync"`
	Install    bool `yaml:"install"`
	Screenshot bool `yaml:"screenshot"`
	GUI        bool `yaml:"gui"`
	Web        bool `yaml:"web"`
	Mobile     bool `yaml:"mobile"`
}

// Target is a single addressable destination.
type Target struct {
	Name      string         `yaml:"name"`
	Transport string         `yaml:"transport"`
	Tags      []string       `yaml:"tags,omitempty"`
	Caps      Caps           `yaml:"capabilities,omitempty"`
	Settings  map[string]any `yaml:",inline"`

	// SourceFile is the file the target was loaded from (set by loader).
	SourceFile string `yaml:"-"`
}

// HasTag returns true if the target carries the given tag.
func (t Target) HasTag(tag string) bool {
	for _, x := range t.Tags {
		if x == tag {
			return true
		}
	}
	return false
}

// Setting returns a typed setting from the inline transport config (e.g.
// `crabbox.host`). Returns nil if the path does not exist.
func (t Target) Setting(path ...string) any {
	var cur any = map[string]any(t.Settings)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
		if cur == nil {
			return nil
		}
	}
	return cur
}

// SettingString returns a string setting or "" if missing.
func (t Target) SettingString(path ...string) string {
	v := t.Setting(path...)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// Registry indexes targets by name with repo-level files overriding user-level.
type Registry struct {
	byName map[string]Target
	order  []string
}

// Load reads all target files from p and returns a registry.
func Load(p config.Paths) (*Registry, error) {
	r := &Registry{byName: map[string]Target{}}
	files, err := p.TargetFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		t, err := readFile(f)
		if err != nil {
			// readFile already prefixes the path; don't double it.
			return nil, err
		}
		if t.Name == "" {
			t.Name = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		}
		if t.Transport == "" {
			return nil, fmt.Errorf("%s: missing transport", f)
		}
		t.SourceFile = f
		if _, exists := r.byName[t.Name]; !exists {
			r.order = append(r.order, t.Name)
		}
		r.byName[t.Name] = t
	}
	sort.Strings(r.order)
	return r, nil
}

func readFile(path string) (Target, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Target{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := schema.ValidateTarget(path, data); err != nil {
		return Target{}, err
	}
	var t Target
	if err := yaml.Unmarshal(data, &t); err != nil {
		return Target{}, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// All returns targets in deterministic order.
func (r *Registry) All() []Target {
	out := make([]Target, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}

// Get returns a target by name.
func (r *Registry) Get(name string) (Target, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Names returns sorted target names.
func (r *Registry) Names() []string {
	out := append([]string(nil), r.order...)
	return out
}

// Save writes a target to disk under userDir/targets/<name>.yaml.
func Save(p config.Paths, t Target) error {
	dir := p.TargetDir[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, t.Name+".yaml")
	data, err := yaml.Marshal(&t)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("target already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Remove deletes a target file (only from user-level dir).
func Remove(p config.Paths, name string) error {
	path := filepath.Join(p.TargetDir[0], name+".yaml")
	return os.Remove(path)
}
