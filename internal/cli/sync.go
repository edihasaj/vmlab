package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var (
		asJSON bool
		paths  []string
	)
	c := &cobra.Command{
		Use:   "sync <instance>",
		Short: "Sync mounts to an instance (parallels: configure shared folders; ssh: rsync)",
		Long: `For parallels instances, sync ensures every declared mount is wired up as a
Parallels shared folder so paths like \\Mac\<name> appear inside the guest.
For ssh-backed instances (Hetzner etc), sync rsyncs each mount's host path
into the guest.

vmlab up already invokes sync for parallels mounts; call it directly when
you've changed an instance's mounts: block and don't want to bounce the VM.`,
		Args: cobra.ExactArgs(1),
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
			pr, err := provider.Default().Get(inst.Provider)
			if err != nil {
				return err
			}
			// Sync needs the instance running. Up is idempotent.
			tgt, _, err := provider.UpEnforced(cmd.Context(), pr, inst)
			if err != nil {
				return err
			}
			tr, err := transport.Default().Get(tgt.Transport)
			if err != nil {
				return err
			}

			results := []map[string]any{}
			sources := mountSources(inst, paths)
			if len(sources) == 0 {
				return fmt.Errorf("sync: instance %q has no mounts and no --path given", inst.Name)
			}
			for _, src := range sources {
				err := tr.Sync(cmd.Context(), tgt, src)
				row := map[string]any{"src": src, "ok": err == nil}
				if err != nil {
					row["error"] = err.Error()
				}
				results = append(results, row)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"instance": inst.Name,
					"results":  results,
				})
			}
			anyErr := false
			for _, r := range results {
				if r["ok"] == true {
					fmt.Fprintf(out, "synced: %s\n", r["src"])
				} else {
					anyErr = true
					fmt.Fprintf(out, "FAILED %s: %s\n", r["src"], r["error"])
				}
			}
			if anyErr {
				return fmt.Errorf("sync: one or more mounts failed")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	c.Flags().StringSliceVar(&paths, "path", nil, "explicit host paths to sync (overrides instance mounts:)")
	return c
}

// mountSources resolves the list of host paths to sync. Flag --path wins; else
// the instance's mounts: block; else nothing.
func mountSources(inst provider.Instance, override []string) []string {
	if len(override) > 0 {
		out := make([]string, 0, len(override))
		for _, p := range override {
			out = append(out, expandHomePath(p))
		}
		return out
	}
	out := make([]string, 0, len(inst.Mounts))
	for _, m := range inst.Mounts {
		out = append(out, expandHomePath(m.Host))
	}
	return out
}

func expandHomePath(p string) string {
	if len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
