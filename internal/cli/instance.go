package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/spf13/cobra"
)

func newInstanceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "instance",
		Short: "Manage provider instances (e.g. parallels VMs, hetzner servers)",
	}
	c.AddCommand(instanceLsCmd(), instanceAddCmd(), instanceRemoveCmd(), instanceShowCmd(), instanceStatusCmd())
	return c
}

func instanceLsCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List configured instances",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := provider.LoadInstances(p)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(r.All())
			}
			if len(r.All()) == 0 {
				fmt.Fprintln(out, "no instances configured. add one with: vmlab instance add ...")
				return nil
			}
			fmt.Fprintf(out, "%-24s %-10s  %s\n", "NAME", "PROVIDER", "TAGS")
			for _, i := range r.All() {
				fmt.Fprintf(out, "%-24s %-10s  %s\n", i.Name, i.Provider, strings.Join(i.Tags, ","))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func instanceAddCmd() *cobra.Command {
	var (
		name     string
		prov     string
		tags     []string
		settings []string
	)
	c := &cobra.Command{
		Use:   "add",
		Short: "Add an instance (writes ~/.vmlab/instances/<name>.yaml)",
		Example: `  vmlab instance add --name win11-studio --provider parallels --tags windows \
      --set parallels.host=mac-studio.local --set parallels.vm='Windows 11' \
      --set target.transport=parallels-guest --set disposition.on_success=suspend`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" || prov == "" {
				return fmt.Errorf("--name and --provider are required")
			}
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(p); err != nil {
				return err
			}
			inst := provider.Instance{
				Name:     name,
				Provider: prov,
				Tags:     tags,
				Settings: map[string]any{},
			}
			for _, kv := range settings {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("invalid --set %q (expected key=value)", kv)
				}
				applyInstanceSet(&inst, strings.Split(k, "."), v)
			}
			if err := provider.SaveInstance(p, inst); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added instance %q (provider=%s)\n", inst.Name, inst.Provider)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "instance name (required)")
	c.Flags().StringVar(&prov, "provider", "", "provider name (required)")
	c.Flags().StringSliceVar(&tags, "tags", nil, "tags, comma-separated")
	c.Flags().StringArrayVar(&settings, "set", nil, "instance setting (key.path=value), repeatable")
	return c
}

func instanceRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Remove a user-level instance",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := provider.RemoveInstance(p, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %q\n", args[0])
			return nil
		},
	}
	return c
}

func instanceShowCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one instance's full config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := provider.LoadInstances(p)
			if err != nil {
				return err
			}
			i, ok := r.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown instance: %s", args[0])
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(i)
			}
			fmt.Fprintf(out, "name: %s\nprovider: %s\ntags: %s\nsource: %s\n", i.Name, i.Provider, strings.Join(i.Tags, ","), i.SourceFile)
			if len(i.Settings) > 0 {
				b, _ := json.MarshalIndent(i.Settings, "", "  ")
				fmt.Fprintf(out, "settings:\n%s\n", string(b))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func instanceStatusCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "status <name>",
		Short: "Show power-state for one instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := provider.LoadInstances(p)
			if err != nil {
				return err
			}
			inst, ok := r.Get(args[0])
			if !ok {
				return fmt.Errorf("unknown instance: %s", args[0])
			}
			reg := provider.Default()
			out := cmd.OutOrStdout()
			pr, err := reg.Get(inst.Provider)
			if err != nil {
				if asJSON {
					return json.NewEncoder(out).Encode(map[string]any{
						"name":     inst.Name,
						"provider": inst.Provider,
						"state":    "unknown",
						"error":    err.Error(),
					})
				}
				fmt.Fprintf(out, "name: %s\nprovider: %s\nstate: unknown\nerror: %v\n", inst.Name, inst.Provider, err)
				return nil
			}
			st, sErr := pr.Status(cmd.Context(), inst)
			msg := ""
			if sErr != nil {
				msg = sErr.Error()
			}
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"name":     inst.Name,
					"provider": inst.Provider,
					"state":    st.String(),
					"error":    msg,
				})
			}
			fmt.Fprintf(out, "name: %s\nprovider: %s\nstate: %s\n", inst.Name, inst.Provider, st.String())
			if msg != "" {
				fmt.Fprintf(out, "error: %s\n", msg)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

// applyInstanceSet maps a dotted --set key onto the right Instance field.
// "tags", "ready.*", "target.*", "disposition.*" land in their typed fields;
// everything else lands in the inline settings map.
func applyInstanceSet(i *provider.Instance, keys []string, value string) {
	if len(keys) == 0 {
		return
	}
	switch keys[0] {
	case "ready":
		if len(keys) >= 2 {
			switch keys[1] {
			case "kind":
				i.Ready.Kind = value
				return
			case "timeout":
				i.Ready.Timeout = value
				return
			}
		}
	case "target":
		if len(keys) >= 2 && keys[1] == "transport" {
			i.Target.Transport = value
			return
		}
	case "disposition":
		if len(keys) >= 2 {
			switch keys[1] {
			case "on_success":
				i.Disp.OnSuccess = value
				return
			case "on_failure":
				i.Disp.OnFailure = value
				return
			case "only_if_we_started":
				i.Disp.OnlyIfWeStarted = strings.ToLower(value) == "true"
				return
			}
		}
	}
	setNested(i.Settings, keys, value)
}
