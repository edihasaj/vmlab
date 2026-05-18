package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/notify"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/spf13/cobra"
)

// notifyHandle bundles a Multi with the per-run metadata every event needs.
// Created once per `with` / `run @<inst>` invocation; events fire through it.
type notifyHandle struct {
	multi    *notify.Multi
	instance provider.Instance
	selector string
	cmd      string
	run      *evidence.Run
}

// loadNotifier reads notify config off ~/.vmlab/config.yaml + repo overrides.
// Returns a no-op handle if disabled, config missing, or `--no-notify` set.
//
// We deliberately swallow load errors and warn on stderr — a malformed notify
// block must never prevent a run from executing.
func loadNotifier(cmd *cobra.Command, paths config.Paths, disabled bool, inst provider.Instance, selector, cmdline string, run *evidence.Run) *notifyHandle {
	h := &notifyHandle{instance: inst, selector: selector, cmd: cmdline, run: run}
	if disabled {
		return h
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()
	m, err := notify.Load(ctx, []string{
		paths.UserFile,
		paths.RepoFile,
		filepath.Join(paths.RepoDir, "config.yaml"),
	})
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "notify: config error: %v (continuing without notifications)\n", err)
		return h
	}
	h.multi = m
	return h
}

// Start fires PhaseStart. Safe to call when multi is nil.
func (h *notifyHandle) Start() {
	if h.multi == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	h.multi.Notify(ctx, h.event(notify.PhaseStart, 0, 0, 0, 0, nil))
}

// Finish fires PhaseSuccess or PhaseFailure based on exit/err. Reads the last
// few lines of the per-target stderr log to include in failure messages.
func (h *notifyHandle) Finish(upMs, runMs, downMs int64, exit int, runErr error) {
	if h.multi == nil {
		return
	}
	phase := notify.PhaseSuccess
	if runErr != nil || exit != 0 {
		phase = notify.PhaseFailure
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	h.multi.Notify(ctx, h.event(phase, upMs, runMs, downMs, exit, runErr))
}

func (h *notifyHandle) event(phase notify.Phase, upMs, runMs, downMs int64, exit int, runErr error) notify.Event {
	ev := notify.Event{
		Phase:    phase,
		Instance: h.instance.Name,
		Provider: h.instance.Provider,
		Selector: h.selector,
		Cmd:      h.cmd,
		UpMs:     upMs,
		RunMs:    runMs,
		DownMs:   downMs,
		ExitCode: exit,
	}
	if runErr != nil {
		ev.Err = runErr.Error()
	}
	if h.run != nil {
		ev.RunID = h.run.ID
		ev.EvidenceDir = h.run.Dir
		if phase == notify.PhaseFailure {
			ev.StderrTail = readStderrTail(h.run.Dir, h.instance.Name)
		}
	}
	return ev
}

// readStderrTail returns up to 6 trailing lines / 800 bytes of the per-target
// stderr log produced during the run. Best-effort — returns "" on any error.
func readStderrTail(runDir, target string) string {
	path := filepath.Join(runDir, "targets", sanitizeForPath(target), "stderr.log")
	const maxRead = 8 * 1024
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	start := st.Size() - maxRead
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	buf := make([]byte, maxRead)
	n, _ := io.ReadFull(f, buf)
	return strings.TrimSpace(string(buf[:n]))
}

// sanitizeForPath mirrors evidence.sanitize: only /, \, and space are
// remapped to '_' — everything else passes through.
func sanitizeForPath(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", " ", "_")
	return r.Replace(s)
}
