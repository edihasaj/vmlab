package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/hooks"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

// runLifecycleHooks executes the hook list for `phase`, teeing output to the
// user's stderr and to <rundir>/hooks-<phase>.log when an evidence bundle is
// active. tr+tgt may be zero-valued for phases that run before Up.
//
// Returns nil if there are no steps; otherwise the first non-ignored error.
func runLifecycleHooks(cmd *cobra.Command, run *evidence.Run, phase hooks.Phase, steps []hooks.Step, tr transport.Transport, tgt target.Target) error {
	if len(steps) == 0 {
		return nil
	}
	out, err := openHookLog(run, phase)
	defer func() {
		if c, ok := out.(io.Closer); ok && c != nil {
			_ = c.Close()
		}
	}()
	teeOut := io.MultiWriter(cmd.OutOrStdout(), out)
	teeErr := io.MultiWriter(cmd.ErrOrStderr(), out)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "hooks: log file unavailable (%v); streaming only\n", err)
	}
	r := &hooks.Runner{Transport: tr, Target: tgt, Stdout: teeOut, Stderr: teeErr}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
	defer cancel()
	return r.Run(ctx, phase, steps)
}

// openHookLog returns a writer for the hook phase's combined output. When run
// is nil it falls back to io.Discard so the helper can be called uniformly.
func openHookLog(run *evidence.Run, phase hooks.Phase) (io.Writer, error) {
	if run == nil {
		return io.Discard, nil
	}
	path := filepath.Join(run.Dir, "hooks-"+string(phase)+".log")
	f, err := os.Create(path)
	if err != nil {
		return io.Discard, err
	}
	return f, nil
}
