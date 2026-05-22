// Transport command — mirrors `vmlab provider` so agents can introspect what
// transports are compiled in and what each one can do. This is the cheapest
// way to teach an LLM the surface: it lists every transport with its
// Capabilities struct fields flipped to a one-line summary.
package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

func newTransportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "transport",
		Aliases: []string{"transports"},
		Short:   "List registered transports and their capabilities",
	}
	c.AddCommand(transportLsCmd(), transportShowCmd())
	return c
}

func transportLsCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registered transports + capabilities",
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg := transport.Default()
			names := reg.Names()
			sort.Strings(names)
			out := cmd.OutOrStdout()
			if asJSON {
				type entry struct {
					Name string         `json:"name"`
					Caps map[string]any `json:"capabilities"`
				}
				entries := make([]entry, 0, len(names))
				for _, n := range names {
					tr, _ := reg.Get(n)
					entries = append(entries, entry{Name: n, Caps: capsToMap(tr.Capabilities())})
				}
				return json.NewEncoder(out).Encode(map[string]any{"transports": entries})
			}
			fmt.Fprintf(out, "%-18s %s\n", "TRANSPORT", "CAPABILITIES")
			for _, n := range names {
				tr, _ := reg.Get(n)
				fmt.Fprintf(out, "%-18s %s\n", n, capsToString(tr.Capabilities()))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func transportShowCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a transport's capabilities and known settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := transport.Default()
			tr, err := reg.Get(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			caps := tr.Capabilities()
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"name":         tr.Name(),
					"capabilities": capsToMap(caps),
					"settings":     transportSettingsDoc[tr.Name()],
				})
			}
			fmt.Fprintf(out, "transport: %s\n", tr.Name())
			fmt.Fprintf(out, "caps:      %s\n", capsToString(caps))
			if doc, ok := transportSettingsDoc[tr.Name()]; ok && len(doc) > 0 {
				fmt.Fprintln(out, "settings:")
				keys := make([]string, 0, len(doc))
				for k := range doc {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(out, "  %-22s %s\n", k, doc[k])
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func capsToString(c transport.Caps) string {
	parts := []string{}
	if c.Shell {
		parts = append(parts, "shell")
	}
	if c.Sync {
		parts = append(parts, "sync")
	}
	if c.Install {
		parts = append(parts, "install")
	}
	if c.Screenshot {
		parts = append(parts, "screenshot")
	}
	if c.GUI {
		parts = append(parts, "gui")
	}
	if c.Web {
		parts = append(parts, "web")
	}
	if c.Mobile {
		parts = append(parts, "mobile")
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

func capsToMap(c transport.Caps) map[string]any {
	return map[string]any{
		"shell":      c.Shell,
		"sync":       c.Sync,
		"install":    c.Install,
		"screenshot": c.Screenshot,
		"gui":        c.GUI,
		"web":        c.Web,
		"mobile":     c.Mobile,
	}
}

// transportSettingsDoc maps transport name → setting key → human description.
// Hand-written so `transport show` reads cleanly. New transports must add an
// entry; an empty map is fine for stateless ones (e.g. local).
var transportSettingsDoc = map[string]map[string]string{
	"local": {},
	"ssh": {
		"ssh.host":       "required — hostname / IP",
		"ssh.user":       "ssh user (default: $USER)",
		"ssh.port":       "ssh port (default: 22)",
		"ssh.identity":   "private key path",
		"ssh.knownHosts": "alt known_hosts file",
		"ssh.strictHost": "yes | no | accept-new",
		"ssh.dest":       "default sync destination",
	},
	"ssh-windows": {
		"ssh.host":       "required",
		"ssh.user":       "default: Administrator",
		"ssh.port":       "default: 22",
		"ssh.identity":   "private key path",
		"ssh.shell":      "pwsh | powershell | cmd | none (default: pwsh)",
		"ssh.strictHost": "yes | no | accept-new",
		"ssh.dest":       "default sync destination (e.g. C:/vmlab)",
	},
	"parallels-guest": {
		"parallels.vm":   "required — Parallels VM name",
		"parallels.host": "remote Mac running Parallels (optional)",
		"parallels.user": "user on the remote Mac",
	},
	"adb": {
		"adb.serial": "device serial (adb devices)",
		"adb.dest":   "default sync destination (default: /sdcard/vmlab)",
	},
	"idb": {
		"idb.udid": "iOS device UDID",
	},
	"simctl": {
		"simctl.udid": "simulator UDID (xcrun simctl list devices)",
	},
	"maestro": {},
	"abx": {
		"abx.mode": "live | headless",
		"abx.url":  "starting URL",
	},
	"guiport": {
		"guiport.app":    "macOS app name",
		"guiport.strict": "fail without screen-recording fallback",
	},
	"crabbox": {
		"crabbox.configPath": "path to crabbox config",
		"crabbox.name":       "named profile in crabbox",
	},
}
