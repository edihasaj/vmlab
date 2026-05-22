package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newWatchCmd is `vmlab watch <selector> <flow-or-cmd> [--src dir]...`.
//
// On each tick (default 1s) it hashes the union of:
//
//   - every file under each --src directory (path, modtime, size)
//   - the flow YAML, if the second arg looks like a flow path
//
// When that hash differs from the previous run, watch invokes vmlab run
// against the same selector and streams its --format=matrix output to
// stdout. When the hash is unchanged, the tick is a no-op (<500ms) —
// the agent sees nothing and pays no token cost.
//
// Why a subprocess (vs. inlining): vmlab run's lifecycle (lock, evidence,
// notifier, hooks, Up/Down) is tightly bound to its RunE closure. Forking
// the binary keeps that single source-of-truth and lets users override
// any per-invocation flag (--retries, --from-snapshot, --json) via the
// passthrough list after `--`.
func newWatchCmd() *cobra.Command {
	var (
		srcs     []string
		interval time.Duration
		once     bool
		extra    []string
	)
	c := &cobra.Command{
		Use:   "watch <selector> <flow-or-cmd>...",
		Short: "Poll source dirs and re-run on change with compact matrix output",
		Long: `Watch <selector> against the supplied flow (or argv command). Every
--interval, watch hashes the --src tree(s) plus the flow YAML. On change,
it invokes vmlab run against the same selector with --format=matrix and
streams the ND-JSON output. Unchanged cycles are silent.

Stop with Ctrl-C. Use --once to run a single tick (after the initial
baseline) and exit — useful for shell loops or CI integration.

Examples:
  vmlab watch @@app-test ./flow.yaml --src ./src --src ./go.mod
  vmlab watch @win11 -- powershell.exe -Command 'Get-ChildItem C:\\app'
  vmlab watch all flows/smoke.yaml --src ./pkg --interval 500ms`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			selectorArg := args[0]
			rest := stripDashDash(args[1:])
			if len(rest) == 0 {
				return fmt.Errorf("watch: missing flow or command after selector")
			}
			if len(srcs) == 0 {
				// Default: watch the current directory. Cheaper than walking
				// the whole repo and matches the common case (editing in the
				// repo you're invoking from).
				srcs = []string{"."}
			}

			// If rest[0] looks like a flow path, fold it into the watch set
			// so editing the flow YAML itself triggers a re-run.
			watchPaths := append([]string(nil), srcs...)
			if isLikelyFlowPath(rest[0]) {
				watchPaths = append(watchPaths, rest[0])
			}

			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			errw := cmd.ErrOrStderr()

			lastHash := ""
			for {
				h, err := hashWatchSet(watchPaths)
				if err != nil {
					return fmt.Errorf("watch: hash inputs: %w", err)
				}
				if h != lastHash {
					fmt.Fprintf(errw, "watch: change detected (hash=%s)\n", short(h))
					runArgs := append([]string{"run", selectorArg}, rest...)
					runArgs = append(runArgs, "--format=matrix")
					runArgs = append(runArgs, extra...)
					if err := execVmlab(ctx, runArgs, out, errw); err != nil {
						// Don't break the watch loop on a single failed run —
						// the next change should still trigger another attempt.
						fmt.Fprintf(errw, "watch: run failed: %v\n", err)
					}
					lastHash = h
					if once {
						return nil
					}
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(interval):
				}
			}
		},
	}
	c.Flags().StringSliceVar(&srcs, "src", nil, "directory or file to watch (repeatable; defaults to current dir)")
	c.Flags().DurationVar(&interval, "interval", time.Second, "poll interval")
	c.Flags().BoolVar(&once, "once", false, "run the first change cycle then exit (useful in scripts / CI)")
	c.Flags().StringSliceVar(&extra, "pass", nil, "extra flag to forward to `vmlab run` (repeatable; e.g. --pass=--from-snapshot=clean)")
	return c
}

// hashWatchSet hashes each file under every path. Directories are walked
// recursively; symlinks are followed only at the top level (matches what
// most CI / editor flows expect). Hidden directories beginning with `.`
// are skipped — that's where lockfiles, .git, .vmlab caches live and they
// churn on every IDE save without representing real source change.
func hashWatchSet(paths []string) (string, error) {
	h := sha256.New()
	type entry struct {
		path string
		mod  int64
		size int64
	}
	var entries []entry
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			// Missing path is allowed (the user may not have created it yet);
			// hash a stable sentinel so it factors in once it appears.
			if os.IsNotExist(err) {
				entries = append(entries, entry{path: p, mod: -1, size: -1})
				continue
			}
			return "", err
		}
		if !info.IsDir() {
			entries = append(entries, entry{path: p, mod: info.ModTime().UnixNano(), size: info.Size()})
			continue
		}
		walkErr := filepath.WalkDir(p, func(sub string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := d.Name()
				if sub != p && strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			entries = append(entries, entry{path: sub, mod: fi.ModTime().UnixNano(), size: fi.Size()})
			return nil
		})
		if walkErr != nil {
			return "", walkErr
		}
	}
	// Sort so the hash is stable regardless of FS walk order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	for _, e := range entries {
		fmt.Fprintf(h, "%s\t%d\t%d\n", e.path, e.mod, e.size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// execVmlab re-invokes the vmlab binary so we reuse run's full lifecycle
// (lock, evidence, notifier, hooks) rather than reimplementing it.
func execVmlab(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		// Fall back to PATH so `go run` and bare `vmlab` both work.
		self = "vmlab"
	}
	c := exec.CommandContext(ctx, self, args...)
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

// short prints the first 12 chars of a hex hash — enough to tell apart
// different runs in the watcher's progress log without filling the line.
func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// isLikelyFlowPath returns true when arg points at a .yaml/.yml file that
// exists on disk. We deliberately keep this less permissive than flow's
// own loader so a stray `flow.yml.bak` doesn't get folded into the watch
// set when the user actually meant a shell command.
func isLikelyFlowPath(arg string) bool {
	if !strings.HasSuffix(arg, ".yaml") && !strings.HasSuffix(arg, ".yml") {
		return false
	}
	info, err := os.Stat(arg)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
