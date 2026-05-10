package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/spf13/cobra"
)

func newTargetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "target",
		Short: "Manage targets",
	}
	c.AddCommand(targetListCmd(), targetAddCmd(), targetRemoveCmd(), targetShowCmd())
	return c
}

func targetListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List configured targets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := target.Load(p)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(r.All())
			}
			out := cmd.OutOrStdout()
			if len(r.All()) == 0 {
				fmt.Fprintln(out, "no targets configured. add one with: vmlab target add ...")
				return nil
			}
			fmt.Fprintf(out, "%-24s %-10s  %s\n", "NAME", "TRANSPORT", "TAGS")
			for _, t := range r.All() {
				fmt.Fprintf(out, "%-24s %-10s  %s\n", t.Name, t.Transport, strings.Join(t.Tags, ","))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func targetAddCmd() *cobra.Command {
	var (
		name      string
		transport string
		tags      []string
		settings  []string
	)
	c := &cobra.Command{
		Use:   "add",
		Short: "Add a target (writes ~/.vmlab/targets/<name>.yaml)",
		Example: `  vmlab target add --name ubuntu-local --transport crabbox --tags linux,vm \
      --set crabbox.configPath=~/.crabbox/ubuntu-local.yaml`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" || transport == "" {
				return fmt.Errorf("--name and --transport are required")
			}
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(p); err != nil {
				return err
			}
			t := target.Target{
				Name:      name,
				Transport: transport,
				Tags:      tags,
				Settings:  map[string]any{},
			}
			for _, kv := range settings {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("invalid --set %q (expected key=value)", kv)
				}
				setNested(t.Settings, strings.Split(k, "."), v)
			}
			if err := target.Save(p, t); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added target %q (transport=%s)\n", t.Name, t.Transport)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "target name (required)")
	c.Flags().StringVar(&transport, "transport", "", "transport name (required)")
	c.Flags().StringSliceVar(&tags, "tags", nil, "tags, comma-separated")
	c.Flags().StringArrayVar(&settings, "set", nil, "transport-specific setting (key.path=value), repeatable")
	return c
}

func targetRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Remove a user-level target",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := target.Remove(p, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %q\n", args[0])
			return nil
		},
	}
	return c
}

func targetShowCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one target's full config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := target.Load(p)
			if err != nil {
				return err
			}
			t, ok := r.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown target: %s", args[0])
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(t)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "name: %s\ntransport: %s\ntags: %s\nsource: %s\n", t.Name, t.Transport, strings.Join(t.Tags, ","), t.SourceFile)
			if len(t.Settings) > 0 {
				b, _ := json.MarshalIndent(t.Settings, "", "  ")
				fmt.Fprintf(out, "settings:\n%s\n", string(b))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

// setNested writes value into nested map by dotted path, creating maps as needed.
func setNested(m map[string]any, keys []string, value string) {
	for i, k := range keys {
		if i == len(keys)-1 {
			m[k] = coerce(value)
			return
		}
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
}

func coerce(v string) any {
	switch strings.ToLower(v) {
	case "true":
		return true
	case "false":
		return false
	}
	return v
}
