package cli

import (
	"encoding/json"
	"fmt"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage instance snapshots (provider-dependent)",
	}
	c.AddCommand(snapshotSaveCmd(), snapshotRestoreCmd(), snapshotListCmd(), snapshotRmCmd())
	return c
}

func resolveSnapshotter(name string) (provider.Snapshotter, provider.Instance, error) {
	pr, inst, err := resolveInstance(name)
	if err != nil {
		return nil, inst, err
	}
	sn, ok := pr.(provider.Snapshotter)
	if !ok {
		return nil, inst, fmt.Errorf("provider %q does not support snapshots", inst.Provider)
	}
	return sn, inst, nil
}

func snapshotSaveCmd() *cobra.Command {
	var description string
	c := &cobra.Command{
		Use:   "save <instance> <name>",
		Short: "Create a snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sn, inst, err := resolveSnapshotter(args[0])
			if err != nil {
				return err
			}
			lock, err := acquireInstanceLock(cmd, inst.Name)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := sn.Snapshot(cmd.Context(), inst, args[1], description); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot saved: %s/%s\n", inst.Name, args[1])
			return nil
		},
	}
	c.Flags().StringVar(&description, "description", "", "optional snapshot description")
	return c
}

func snapshotRestoreCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "restore <instance> <name>",
		Short: "Switch to a named snapshot (destructive)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sn, inst, err := resolveSnapshotter(args[0])
			if err != nil {
				return err
			}
			lock, err := acquireInstanceLock(cmd, inst.Name)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := sn.Restore(cmd.Context(), inst, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot restored: %s/%s\n", inst.Name, args[1])
			return nil
		},
	}
	return c
}

func snapshotListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "ls <instance>",
		Aliases: []string{"list"},
		Short:   "List snapshots for an instance",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sn, inst, err := resolveSnapshotter(args[0])
			if err != nil {
				return err
			}
			snaps, err := sn.ListSnapshots(cmd.Context(), inst)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(snaps)
			}
			if len(snaps) == 0 {
				fmt.Fprintf(out, "no snapshots for %s\n", inst.Name)
				return nil
			}
			fmt.Fprintf(out, "%-20s %-8s %-22s %s\n", "NAME", "CURRENT", "DATE", "ID")
			for _, s := range snaps {
				cur := ""
				if s.Current {
					cur = "*"
				}
				fmt.Fprintf(out, "%-20s %-8s %-22s %s\n", s.Name, cur, s.Date, s.ID)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

func snapshotRmCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <instance> <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete a named snapshot",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sn, inst, err := resolveSnapshotter(args[0])
			if err != nil {
				return err
			}
			lock, err := acquireInstanceLock(cmd, inst.Name)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := sn.DeleteSnapshot(cmd.Context(), inst, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot deleted: %s/%s\n", inst.Name, args[1])
			return nil
		},
	}
	return c
}
