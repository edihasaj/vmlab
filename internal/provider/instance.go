package provider

import "github.com/edihasaj/vmlab/internal/hooks"

// Instance is the declarative input every Provider operates on. It is loaded
// from ~/.vmlab/instances/<name>.yaml (and repo overrides) and validated
// against schema/instance.schema.json.
type Instance struct {
	Name     string         `yaml:"name"`
	Provider string         `yaml:"provider"`
	Tags     []string       `yaml:"tags,omitempty"`
	Ready    ReadyConfig    `yaml:"ready,omitempty"`
	Target   TargetConfig   `yaml:"target,omitempty"`
	Disp     DispositionCfg `yaml:"disposition,omitempty"`
	Mounts   []Mount        `yaml:"mounts,omitempty"`
	Hooks    hooks.Config   `yaml:"hooks,omitempty"`
	Budget   BudgetCfg      `yaml:"budget,omitempty"`
	Settings map[string]any `yaml:",inline"`

	// SourceFile is set by the loader.
	SourceFile string `yaml:"-"`
}

// Mount declares a host-to-guest file share. Provider semantics:
//   - parallels: creates a Parallels shared folder; visible as `\\Mac\<name>`
//     in Windows guests. Auto-configured on Up. When FromLaptop is true and
//     parallels.host is set, vmlab rsyncs Host (resolved on the laptop) to
//     a cache dir on the remote Parallels host before --shf-host-add.
//   - hetzner / ssh: `vmlab sync` rsyncs Host into Guest on demand.
type Mount struct {
	Name       string `yaml:"name"`                  // share name; defaults to basename of host
	Host       string `yaml:"host"`                  // host path (tilde-expanded)
	Guest      string `yaml:"guest,omitempty"`       // informational guest path
	Mode       string `yaml:"mode,omitempty"`        // ro | rw  (default rw)
	FromLaptop bool   `yaml:"from_laptop,omitempty"` // rsync laptop:Host → parallels.host:cache before --shf-host-add
}

// ReadyConfig describes how the provider decides "ready for traffic".
type ReadyConfig struct {
	Kind    string `yaml:"kind,omitempty"`    // parallels-tools | ssh | tcp:22 | http
	Timeout string `yaml:"timeout,omitempty"` // e.g. "120s"
}

// TargetConfig describes the transport-side shape emitted by Up.
type TargetConfig struct {
	Transport string `yaml:"transport,omitempty"`
}

// BudgetCfg sets a soft cost ceiling vmlab refuses to cross when bringing
// an instance up. Providers that know their per-instance hourly rate (or
// can be told one via budget.hourlyUSD) compare against the cap before
// firing the create / start API call. Mostly aimed at cloud providers
// where a misconfigured instance type or region quietly bills 100× more
// than the operator intended.
//
// Fields:
//   - HourlyUSD:  cap in USD/hour. Provider Up returns ErrBudgetExceeded
//     if the resolved per-instance rate is higher than this.
//   - DailyUSD:   not enforced yet — reserved for the future "sum of
//     this calendar day's runs" check once we have aggregate accounting.
//
// Cost data the provider derives itself (instance type → price table,
// AWS pricing API, etc.) is consulted first; the operator can override
// via the same struct's HourlyUSD when the provider has no source.
type BudgetCfg struct {
	HourlyUSD float64 `yaml:"hourlyUSD,omitempty"`
	DailyUSD  float64 `yaml:"dailyUSD,omitempty"`
}

// DispositionCfg controls what `vmlab with` and the flow-level bookends do
// on success / failure paths.
type DispositionCfg struct {
	OnSuccess       string `yaml:"on_success,omitempty"`
	OnFailure       string `yaml:"on_failure,omitempty"`
	OnlyIfWeStarted bool   `yaml:"only_if_we_started,omitempty"`
}

// HasTag returns true if the instance carries the given tag.
func (i Instance) HasTag(tag string) bool {
	for _, x := range i.Tags {
		if x == tag {
			return true
		}
	}
	return false
}

// Setting returns a typed setting from the inline provider config (e.g.
// `parallels.host`). Returns nil if the path does not exist.
func (i Instance) Setting(path ...string) any {
	var cur any = map[string]any(i.Settings)
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
func (i Instance) SettingString(path ...string) string {
	v := i.Setting(path...)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
