package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/flow"
	"github.com/edihasaj/vmlab/internal/hooks"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

// newImageCmd groups image-related subcommands. `vmlab image build` is the
// only one today; `image ls` / `image rm` follow once the use case warrants.
func newImageCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "image",
		Short: "Manage provider images / snapshots baked from flows",
	}
	c.AddCommand(newImageBuildCmd(), newImageListCmd(), newImageDeleteCmd())
	return c
}

func newImageBuildCmd() *cobra.Command {
	var (
		name        string
		description string
		keep        bool
	)
	c := &cobra.Command{
		Use:   "build @<instance> <flow.yaml>",
		Short: "Bring up an instance, run a flow, snapshot the result, then destroy",
		Long: `Bakes a provider snapshot/image from the supplied flow. Sequence:

  1. Up @<instance>      (creates a fresh VM — flow expects a clean slate)
  2. post_up hooks
  3. Run the flow
  4. Provider.Snapshot(--name)
  5. Down @<instance>    (destroy by default; --keep to leave running)

The named snapshot is then available for future Up calls. For Hetzner the
snapshot is recorded as an image tagged vmlab-image=<name>; for Parallels
it's a named PRL snapshot.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			instArg := args[0]
			flowPath := args[1]
			if name == "" {
				return fmt.Errorf("--name <image-name> is required")
			}
			if !strings.HasPrefix(instArg, "@") {
				return fmt.Errorf("first arg must be @<instance>")
			}
			instName := strings.TrimPrefix(instArg, "@")

			pr, inst, err := resolveInstance(instName)
			if err != nil {
				return err
			}
			sn, ok := pr.(provider.Snapshotter)
			if !ok {
				return fmt.Errorf("provider %q does not implement snapshots", inst.Provider)
			}

			_, paths, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(paths); err != nil {
				return err
			}
			lock, err := acquireInstanceLockAt(cmd, paths, inst.Name)
			if err != nil {
				return err
			}
			defer lock.Release()

			f, err := flow.Load(flowPath)
			if err != nil {
				return err
			}

			run, err := evidence.New(paths.RunsDir)
			if err != nil {
				return err
			}
			run.SetFlow(f.SourceFile)
			run.SetSelector("@" + inst.Name + " image:" + name)
			_ = run.MarkRunning()

			nfy := loadNotifier(cmd, paths, false, inst, "image:"+name, f.SourceFile, run)
			nfy.Start()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Minute)
			defer cancel()

			// Up
			upStart := time.Now()
			tgt, ensure, upErr := pr.Up(ctx, inst)
			upMs := time.Since(upStart).Milliseconds()
			if upErr != nil {
				nfy.Finish(upMs, 0, 0, 1, upErr)
				return upErr
			}
			tr, err := transport.Default().Get(tgt.Transport)
			if err != nil {
				_ = pr.Down(context.Background(), inst, provider.DisposeDestroy)
				nfy.Finish(upMs, 0, 0, 1, err)
				return err
			}
			o, e, l, lerr := run.TargetWriters(inst.Name, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if lerr != nil {
				return lerr
			}
			defer l.Close()
			teeOut, teeErr := o, e

			// post_up hooks (transport ready)
			if err := (&hooks.Runner{Transport: tr, Target: tgt, Stdout: teeOut, Stderr: teeErr}).
				Run(ctx, hooks.PhasePostUp, inst.Hooks.PostUp); err != nil {
				cleanupImage(ctx, pr, inst, keep, teeErr)
				nfy.Finish(upMs, 0, 0, 1, err)
				return err
			}

			// Run flow
			runStart := time.Now()
			steps, runErr := flow.Run(ctx, tr, tgt, f, teeOut, teeErr)
			runMs := time.Since(runStart).Milliseconds()
			_, _ = run.WriteSteps(inst.Name, steps)
			exit := lastExit(steps, runErr)

			if runErr != nil || exit != 0 {
				cleanupImage(ctx, pr, inst, keep, teeErr)
				_ = ensure // silence unused
				nfy.Finish(upMs, runMs, 0, exit, runErr)
				return fmt.Errorf("flow failed (exit=%d): %w", exit, runErr)
			}

			// Snapshot
			fmt.Fprintf(cmd.ErrOrStderr(), "\nbaking image %q via %s.Snapshot…\n", name, inst.Provider)
			snapStart := time.Now()
			if err := sn.Snapshot(ctx, inst, name, description); err != nil {
				cleanupImage(ctx, pr, inst, keep, teeErr)
				nfy.Finish(upMs, runMs, time.Since(snapStart).Milliseconds(), 1, err)
				return fmt.Errorf("snapshot %q: %w", name, err)
			}
			snapMs := time.Since(snapStart).Milliseconds()

			// Cleanup
			downStart := time.Now()
			cleanupImage(ctx, pr, inst, keep, teeErr)
			downMs := time.Since(downStart).Milliseconds()

			run.AddTarget(evidence.TargetSummary{
				Name:      inst.Name,
				Transport: tgt.Transport,
				ExitCode:  0,
				Duration:  runMs,
			})
			meta, _ := run.Finish(0)
			fmt.Fprintf(cmd.OutOrStdout(), "\nimage built: %s (provider=%s, run-id=%s)\n  up=%dms run=%dms snap=%dms down=%dms\n",
				name, inst.Provider, meta.ID, upMs, runMs, snapMs, downMs)
			nfy.Finish(upMs, runMs+snapMs, downMs, 0, nil)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "image name (required); used to look up the snapshot later")
	c.Flags().StringVar(&description, "description", "", "human-readable description stored alongside the image")
	c.Flags().BoolVar(&keep, "keep", false, "leave the source instance running instead of destroying it after snapshot")
	return c
}

func cleanupImage(ctx context.Context, pr provider.Provider, inst provider.Instance, keep bool, errOut io.Writer) {
	if keep {
		fmt.Fprintf(errOut, "image build: --keep set, leaving instance running\n")
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := pr.Down(cctx, inst, provider.DisposeDestroy); err != nil {
		fmt.Fprintf(errOut, "image build: cleanup failed: %v\n", err)
	}
}

func newImageListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ls @<instance>",
		Short: "List snapshots/images registered against the instance's provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instArg := strings.TrimPrefix(args[0], "@")
			pr, inst, err := resolveInstance(instArg)
			if err != nil {
				return err
			}
			sn, ok := pr.(provider.Snapshotter)
			if !ok {
				return fmt.Errorf("provider %q does not implement snapshots", inst.Provider)
			}
			snaps, err := sn.ListSnapshots(cmd.Context(), inst)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(snaps) == 0 {
				fmt.Fprintln(out, "no images")
				return nil
			}
			fmt.Fprintf(out, "%-12s %-24s %-20s %s\n", "ID", "NAME", "DATE", "STATE")
			for _, s := range snaps {
				fmt.Fprintf(out, "%-12s %-24s %-20s %s\n", s.ID, s.Name, s.Date, s.State)
			}
			return nil
		},
	}
	return c
}

func newImageDeleteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "rm @<instance> <image-name>",
		Short: "Delete a snapshot/image by name from the instance's provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			instArg := strings.TrimPrefix(args[0], "@")
			pr, inst, err := resolveInstance(instArg)
			if err != nil {
				return err
			}
			sn, ok := pr.(provider.Snapshotter)
			if !ok {
				return fmt.Errorf("provider %q does not implement snapshots", inst.Provider)
			}
			if err := sn.DeleteSnapshot(cmd.Context(), inst, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted image %s on %s\n", args[1], inst.Provider)
			_ = os.Stderr
			return nil
		},
	}
	return c
}
