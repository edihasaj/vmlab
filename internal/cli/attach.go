package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/spf13/cobra"
)

// newAttachCmd tails every per-target log inside a running run dir until
// the run finishes (running.lock disappears). Operates on log files on
// disk so multiple terminals can attach without contending for stdout
// from the live process.
func newAttachCmd() *cobra.Command {
	var pollMs int
	c := &cobra.Command{
		Use:   "attach <run-id>",
		Short: "Live-tail a running run's per-target logs",
		Long: `Walks <runs-dir>/<run-id>/targets/* and tails stdout.log + stderr.log
for every target until the run completes (running.lock is removed). Safe to
attach from multiple terminals.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			runDir := filepath.Join(cfg.RunsDir, args[0])
			if _, err := os.Stat(runDir); err != nil {
				return fmt.Errorf("run %s: %w", args[0], err)
			}
			out := cmd.OutOrStdout()

			// Show the current running.lock contents up-front so the user knows
			// what they're attaching to.
			if st, err := evidence.ReadRunningState(runDir); err == nil {
				fmt.Fprintf(out, "attached: pid=%d started=%s cmd=%q\n",
					st.PID, st.Started.Local().Format(time.RFC3339), st.Cmd)
			} else if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(out, "run already finished — replaying logs once")
			}

			targets, _ := os.ReadDir(filepath.Join(runDir, "targets"))
			tails := make([]*logTail, 0, len(targets)*2)
			for _, e := range targets {
				if !e.IsDir() {
					continue
				}
				for _, fn := range []string{"stdout.log", "stderr.log"} {
					tails = append(tails, &logTail{path: filepath.Join(runDir, "targets", e.Name(), fn), label: "[" + e.Name() + " " + fn[:len(fn)-4] + "] ", out: out})
				}
			}

			interval := time.Duration(pollMs) * time.Millisecond
			if interval <= 0 {
				interval = 200 * time.Millisecond
			}

			for {
				for _, t := range tails {
					_ = t.drain()
				}
				if !runIsLive(runDir) {
					// One last drain so we catch the final lines, then exit.
					for _, t := range tails {
						_ = t.drain()
					}
					fmt.Fprintln(out, "\n— run finished —")
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(interval):
				}
			}
		},
	}
	c.Flags().IntVar(&pollMs, "poll-ms", 200, "log poll interval")
	return c
}

// newCancelCmd reads running.lock and sends SIGINT to the recorded PID.
// SIGINT (rather than SIGKILL) so the in-process defer/cleanup logic still
// runs — instances get suspended/destroyed per their disposition.
func newCancelCmd() *cobra.Command {
	var signalName string
	c := &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Send SIGINT (or --signal) to a running run, letting cleanup hooks fire",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			runDir := filepath.Join(cfg.RunsDir, args[0])
			st, err := evidence.ReadRunningState(runDir)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("run %s: not running (no running.lock)", args[0])
				}
				return err
			}
			sig := syscall.SIGINT
			switch signalName {
			case "", "INT", "SIGINT":
				sig = syscall.SIGINT
			case "TERM", "SIGTERM":
				sig = syscall.SIGTERM
			case "KILL", "SIGKILL":
				sig = syscall.SIGKILL
			default:
				return fmt.Errorf("unknown signal %q (use INT|TERM|KILL)", signalName)
			}
			if err := syscall.Kill(st.PID, sig); err != nil {
				return fmt.Errorf("kill pid=%d: %w", st.PID, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sent %s to pid=%d (run %s)\n", sig, st.PID, args[0])
			return nil
		},
	}
	c.Flags().StringVar(&signalName, "signal", "INT", "INT | TERM | KILL")
	return c
}

func runIsLive(runDir string) bool {
	_, err := os.Stat(filepath.Join(runDir, "running.lock"))
	return err == nil
}

// logTail walks a single file from its last-read offset, printing every new
// chunk under the given label. Best-effort: missing files are silently
// skipped so attach works the moment a run dir exists, even if a target
// hasn't written its first byte yet.
type logTail struct {
	path   string
	label  string
	offset int64
	out    io.Writer
}

func (t *logTail) drain() error {
	f, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, 16*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			pw := &prefixWriter{w: t.out, prefix: t.label}
			_, _ = pw.Write(buf[:n])
			t.offset += int64(n)
		}
		if errors.Is(err, io.EOF) || err != nil {
			break
		}
	}
	return nil
}
