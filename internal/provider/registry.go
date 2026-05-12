package provider

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

// InstanceRegistry indexes loaded instances by name.
type InstanceRegistry struct {
	byName map[string]Instance
	order  []string
}

// LoadInstances reads every instance file from p and validates each one
// against the instance JSON schema.
func LoadInstances(p config.Paths) (*InstanceRegistry, error) {
	r := &InstanceRegistry{byName: map[string]Instance{}}
	files, err := p.InstanceFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		i, err := readInstanceFile(f)
		if err != nil {
			return nil, err
		}
		if i.Name == "" {
			i.Name = strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		}
		if i.Provider == "" {
			return nil, fmt.Errorf("%s: missing provider", f)
		}
		i.SourceFile = f
		if _, exists := r.byName[i.Name]; !exists {
			r.order = append(r.order, i.Name)
		}
		r.byName[i.Name] = i
	}
	sort.Strings(r.order)
	return r, nil
}

func readInstanceFile(path string) (Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Instance{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := schema.ValidateInstance(path, data); err != nil {
		return Instance{}, err
	}
	var i Instance
	if err := yaml.Unmarshal(data, &i); err != nil {
		return Instance{}, fmt.Errorf("%s: %w", path, err)
	}
	return i, nil
}

// All returns instances in deterministic order.
func (r *InstanceRegistry) All() []Instance {
	out := make([]Instance, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}

// Get returns an instance by name.
func (r *InstanceRegistry) Get(name string) (Instance, bool) {
	i, ok := r.byName[name]
	return i, ok
}

// Names returns sorted instance names.
func (r *InstanceRegistry) Names() []string {
	out := append([]string(nil), r.order...)
	return out
}

// SaveInstance writes an instance to disk under userDir/instances/<name>.yaml.
func SaveInstance(p config.Paths, i Instance) error {
	dir := p.InstanceDir[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, i.Name+".yaml")
	data, err := yaml.Marshal(&i)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("instance already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// RemoveInstance deletes an instance file from the user-level dir.
func RemoveInstance(p config.Paths, name string) error {
	path := filepath.Join(p.InstanceDir[0], name+".yaml")
	return os.Remove(path)
}
