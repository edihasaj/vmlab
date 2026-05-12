// Package config loads vmlab configuration from ~/.vmlab and per-repo .vmlab.yaml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Config is the root config for vmlab.
type Config struct {
	// RunsDir defaults to <home>/.vmlab/runs.
	RunsDir string `yaml:"runsDir,omitempty"`
	// EvidenceRetentionDays defaults to 30.
	EvidenceRetentionDays int `yaml:"evidenceRetentionDays,omitempty"`
	// DefaultMaxParallel for fan-out (0 = unlimited).
	DefaultMaxParallel int `yaml:"defaultMaxParallel,omitempty"`
}

// DefaultConfig returns built-in defaults.
func DefaultConfig() Config {
	return Config{
		EvidenceRetentionDays: 30,
		DefaultMaxParallel:    0,
	}
}

// Paths returns the resolved config and target directories, merged in
// precedence order: built-in defaults < user (~/.vmlab) < repo (.vmlab/).
type Paths struct {
	UserDir   string // ~/.vmlab
	UserFile  string // ~/.vmlab/config.yaml
	RepoDir   string // <cwd>/.vmlab
	RepoFile  string // <cwd>/.vmlab.yaml or <cwd>/.vmlab/config.yaml
	RunsDir     string
	TargetDir   []string // ordered: user first, repo overrides
	InstanceDir []string // ordered: user first, repo overrides
}

// ResolvePaths returns the canonical paths used by vmlab.
func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("home dir: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Paths{}, fmt.Errorf("cwd: %w", err)
	}
	userDir := filepath.Join(home, ".vmlab")
	repoDir := filepath.Join(cwd, ".vmlab")
	p := Paths{
		UserDir:  userDir,
		UserFile: filepath.Join(userDir, "config.yaml"),
		RepoDir:  repoDir,
		RepoFile: filepath.Join(cwd, ".vmlab.yaml"),
		RunsDir:  filepath.Join(userDir, "runs"),
		TargetDir: []string{
			filepath.Join(userDir, "targets"),
			filepath.Join(repoDir, "targets"),
		},
		InstanceDir: []string{
			filepath.Join(userDir, "instances"),
			filepath.Join(repoDir, "instances"),
		},
	}
	return p, nil
}

// Load merges defaults with user and repo configs.
func Load() (Config, Paths, error) {
	p, err := ResolvePaths()
	if err != nil {
		return Config{}, Paths{}, err
	}
	cfg := DefaultConfig()
	for _, file := range []string{p.UserFile, p.RepoFile, filepath.Join(p.RepoDir, "config.yaml")} {
		if err := mergeFile(&cfg, file); err != nil {
			return Config{}, Paths{}, err
		}
	}
	if cfg.RunsDir == "" {
		cfg.RunsDir = p.RunsDir
	} else {
		p.RunsDir = expand(cfg.RunsDir)
	}
	return cfg, p, nil
}

func mergeFile(into *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if c.RunsDir != "" {
		into.RunsDir = c.RunsDir
	}
	if c.EvidenceRetentionDays != 0 {
		into.EvidenceRetentionDays = c.EvidenceRetentionDays
	}
	if c.DefaultMaxParallel != 0 {
		into.DefaultMaxParallel = c.DefaultMaxParallel
	}
	return nil
}

// EnsureDirs creates user-level dirs if missing.
func EnsureDirs(p Paths) error {
	for _, d := range []string{
		p.UserDir,
		filepath.Join(p.UserDir, "targets"),
		filepath.Join(p.UserDir, "instances"),
		p.RunsDir,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// TargetFiles returns all .yaml/.yml files under TargetDir, repo-level last.
func (p Paths) TargetFiles() ([]string, error) {
	return collectYAML(p.TargetDir)
}

// InstanceFiles returns all .yaml/.yml files under InstanceDir, repo-level last.
func (p Paths) InstanceFiles() ([]string, error) {
	return collectYAML(p.InstanceDir)
}

func collectYAML(dirs []string) ([]string, error) {
	var out []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var batch []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if filepath.Ext(n) != ".yaml" && filepath.Ext(n) != ".yml" {
				continue
			}
			batch = append(batch, filepath.Join(dir, n))
		}
		sort.Strings(batch)
		out = append(out, batch...)
	}
	return out, nil
}

// expand ~/foo to $HOME/foo.
func expand(p string) string {
	if len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == os.PathSeparator) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// ExpandPath is the exported version of expand.
func ExpandPath(p string) string { return expand(p) }
