package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// newMatrixCmd groups the agent-friendly matrix surface. `matrix run` is
// the single entry-point teams point at: one selector, one flow (or argv),
// optional --watch to loop on source change, and the same --format=matrix
// output every other vmlab path already speaks. The command is sugar over
// `vmlab run --format=matrix` (and `vmlab watch` when --watch is set) so
// the underlying lifecycle / lock / evidence / notifier code stays single-
// sourced.
func newMatrixCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "matrix",
		Short: "Cross-OS run + watch with one compact ND-JSON row per target",
	}
	c.AddCommand(newMatrixRunCmd())
	return c
}

func newMatrixRunCmd() *cobra.Command {
	var (
		watch        bool
		srcs         []string
		interval     time.Duration
		once         bool
		fromSnapshot string
		retries      int
		extra        []string
	)
	c := &cobra.Command{
		Use:   "run <selector> <flow-or-cmd>...",
		Short: "Run with --format=matrix; --watch re-runs on every source change",
		Long: `One-shot or loop-mode matrix run.

  vmlab matrix run @@app-test ./flow.yaml --from-snapshot=clean
  vmlab matrix run @@app-test ./flow.yaml --watch --src ./src

Without --watch this is identical to:
  vmlab run <selector> <flow-or-cmd> --format=matrix [...]

With --watch it delegates to ` + "`vmlab watch`" + ` (poll --src trees, re-invoke
vmlab run --format=matrix on every hash flip). Unchanged ticks finish in
<1ms — agents pay essentially zero tokens between code edits.

Forward extra flags to the underlying ` + "`vmlab run`" + ` via --pass (repeatable),
e.g. --pass=--max-parallel=2.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			selectorArg := args[0]
			rest := stripDashDash(args[1:])
			if len(rest) == 0 {
				return fmt.Errorf("matrix run: missing flow or command after selector")
			}

			if watch {
				// Delegate to `vmlab watch` so the watch loop's hash gate
				// and subprocess dispatch stay single-sourced. Anything the
				// user wants passed through to `vmlab run` rides on --pass.
				wargs := []string{"watch", selectorArg}
				wargs = append(wargs, rest...)
				for _, s := range srcs {
					wargs = append(wargs, "--src", s)
				}
				if interval > 0 {
					wargs = append(wargs, "--interval", interval.String())
				}
				if once {
					wargs = append(wargs, "--once")
				}
				if fromSnapshot != "" {
					wargs = append(wargs, "--pass=--from-snapshot="+fromSnapshot)
				}
				if retries > 0 {
					wargs = append(wargs, "--pass=--retries="+strconv.Itoa(retries))
				}
				for _, e := range extra {
					wargs = append(wargs, "--pass="+e)
				}
				return execVmlab(cmd.Context(), wargs, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}

			// Non-watch: straight passthrough to `vmlab run … --format=matrix`.
			runArgs := []string{"run", selectorArg}
			runArgs = append(runArgs, rest...)
			runArgs = append(runArgs, "--format=matrix")
			if fromSnapshot != "" {
				runArgs = append(runArgs, "--from-snapshot", fromSnapshot)
			}
			if retries > 0 {
				runArgs = append(runArgs, "--retries", strconv.Itoa(retries))
			}
			runArgs = append(runArgs, extra...)
			return execVmlab(cmd.Context(), runArgs, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	c.Flags().BoolVar(&watch, "watch", false, "loop: re-run on every source change (delegates to vmlab watch)")
	c.Flags().StringSliceVar(&srcs, "src", nil, "directory or file to watch (repeatable; only used with --watch)")
	c.Flags().DurationVar(&interval, "interval", time.Second, "watch poll interval (only used with --watch)")
	c.Flags().BoolVar(&once, "once", false, "with --watch: run the first change cycle then exit")
	c.Flags().StringVar(&fromSnapshot, "from-snapshot", "", "restore named snapshot before Up (forwarded to run)")
	c.Flags().IntVar(&retries, "retries", 0, "per-target retries on failure (forwarded to run)")
	c.Flags().StringSliceVar(&extra, "pass", nil, "extra flag forwarded to the underlying run command (repeatable)")
	return c
}
