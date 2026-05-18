package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/edihasaj/vmlab/internal/credresolver"
	"gopkg.in/yaml.v3"
)

// FileConfig mirrors the `notify:` block in ~/.vmlab/config.yaml.
//
//	notify:
//	  discord:
//	    webhook: op://Personal/vmlab-discord-runs/credential
//	    on: [start, success, failure]
//	    mention_on_failure: "<@123456789>"
//	    username: vmlab
type FileConfig struct {
	Discord *DiscordConfig `yaml:"discord,omitempty"`
}

// DiscordConfig is the YAML shape for the Discord adapter.
type DiscordConfig struct {
	Webhook          string   `yaml:"webhook"`
	On               []string `yaml:"on,omitempty"`
	MentionOnFailure string   `yaml:"mention_on_failure,omitempty"`
	Username         string   `yaml:"username,omitempty"`
}

// rootConfigFile is the YAML wrapper we read off disk. We only care about the
// notify subtree; everything else is preserved by yaml.Node round-tripping but
// we don't model it here.
type rootConfigFile struct {
	Notify FileConfig `yaml:"notify,omitempty"`
}

// Load reads notify config from the given vmlab config files, in precedence
// order (later wins). Missing files are not errors. Returns a Multi with all
// configured notifiers, or an empty Multi if nothing is configured.
func Load(ctx context.Context, configFiles []string) (*Multi, error) {
	var fc FileConfig
	for _, f := range configFiles {
		if f == "" {
			continue
		}
		if err := mergeFile(&fc, f); err != nil {
			return nil, err
		}
	}
	return Build(ctx, fc)
}

// Build turns a parsed FileConfig into a Multi. Exposed for tests + cases
// where the caller already has structured config in hand.
func Build(ctx context.Context, fc FileConfig) (*Multi, error) {
	m := &Multi{}
	if fc.Discord != nil && fc.Discord.Webhook != "" {
		hook, err := credresolver.Resolve(ctx, fc.Discord.Webhook)
		if err != nil {
			return nil, fmt.Errorf("notify.discord.webhook: %w", err)
		}
		d := NewDiscord(hook, fc.Discord.MentionOnFailure)
		d.UsernameOverride = fc.Discord.Username
		phases, err := parsePhases(fc.Discord.On)
		if err != nil {
			return nil, fmt.Errorf("notify.discord.on: %w", err)
		}
		m.Notifiers = append(m.Notifiers, &phasedNotifier{inner: d, phases: phases})
	}
	return m, nil
}

func mergeFile(into *FileConfig, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	var root rootConfigFile
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if root.Notify.Discord != nil {
		into.Discord = root.Notify.Discord
	}
	return nil
}

func parsePhases(list []string) (map[Phase]bool, error) {
	if len(list) == 0 {
		return phasesAll(), nil
	}
	out := map[Phase]bool{}
	for _, s := range list {
		p, ok := ParsePhase(s)
		if !ok {
			return nil, fmt.Errorf("unknown phase: %q (want start|success|failure)", s)
		}
		out[p] = true
	}
	return out, nil
}

// phasedNotifier wraps a Notifier to override its Phases() with a config-driven
// allow-list. Keeps adapter code phase-agnostic.
type phasedNotifier struct {
	inner  Notifier
	phases map[Phase]bool
}

func (p *phasedNotifier) Name() string                          { return p.inner.Name() }
func (p *phasedNotifier) Phases() map[Phase]bool                { return p.phases }
func (p *phasedNotifier) Notify(ctx context.Context, ev Event) error {
	return p.inner.Notify(ctx, ev)
}

// FindUserConfigFile returns the conventional path ~/.vmlab/config.yaml so
// callers without access to the config package's Paths can still drive Load.
// Returns "" if the home dir can't be resolved.
func FindUserConfigFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vmlab", "config.yaml")
}
