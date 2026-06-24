// Package project resolves "project profiles" — a saved mapping from a local
// repo to the target + flow that verifies it. It lets `vmlab verify` infer
// what to run from the working directory (or an explicit name) so callers
// don't have to remember which flow/target pairs with which repo.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"gopkg.in/yaml.v3"
)

// Profile maps a project to how it should be verified. Stored as
// ~/.vmlab/projects/<name>.yaml (or .vmlab/projects/ in a repo).
type Profile struct {
	// Name is the profile's identity, used by `vmlab verify <name>`.
	Name string `yaml:"name"`
	// Path is the local repo root. When the working directory is at or under
	// this path, the profile is auto-selected (longest match wins).
	Path string `yaml:"path,omitempty"`
	// Target is the selector passed to the run (e.g. "win11-ssh", "@linux").
	Target string `yaml:"target"`
	// Flow is the path to the flow YAML to run against Target.
	Flow string `yaml:"flow"`

	// SourceFile is the file the profile was loaded from (set by the loader).
	SourceFile string `yaml:"-"`
}

// ExpandedPath returns Path with a leading ~ resolved to the home dir.
func (pr Profile) ExpandedPath() string { return expandHome(pr.Path) }

// ExpandedFlow returns Flow with a leading ~ resolved to the home dir.
func (pr Profile) ExpandedFlow() string { return expandHome(pr.Flow) }

// Load reads every project profile under the configured project dirs.
func Load(p config.Paths) ([]Profile, error) {
	files, err := p.ProjectFiles()
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		var pr Profile
		if err := yaml.Unmarshal(data, &pr); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		if pr.Name == "" {
			// Fall back to the file stem so a profile is always addressable.
			base := filepath.Base(f)
			pr.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		pr.SourceFile = f
		profiles = append(profiles, pr)
	}
	return profiles, nil
}

// ByName returns the profile with the given name.
func ByName(profiles []Profile, name string) (Profile, bool) {
	for _, pr := range profiles {
		if pr.Name == name {
			return pr, true
		}
	}
	return Profile{}, false
}

// Detect returns the profile whose Path is the deepest ancestor of dir (or dir
// itself). Longest match wins so a nested repo beats its parent.
func Detect(profiles []Profile, dir string) (Profile, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	matches := make([]Profile, 0, 2)
	for _, pr := range profiles {
		root := pr.ExpandedPath()
		if root == "" {
			continue
		}
		if r, err := filepath.Abs(root); err == nil {
			root = r
		}
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			matches = append(matches, pr)
		}
	}
	if len(matches) == 0 {
		return Profile{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return len(matches[i].ExpandedPath()) > len(matches[j].ExpandedPath())
	})
	return matches[0], true
}

func expandHome(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if s == "~" {
				return home
			}
			return filepath.Join(home, s[2:])
		}
	}
	return s
}
