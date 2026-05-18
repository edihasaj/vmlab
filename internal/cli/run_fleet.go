package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/flow"
	"github.com/edihasaj/vmlab/internal/hooks"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

// instanceClassShortcut parses `@@<tag>` and returns every configured
// instance carrying that tag. Falls back to (nil, false) for any other
// selector shape so the caller can try the @<name> path next.
func instanceClassShortcut(selectorArg string, p config.Paths) ([]provider.Instance, bool) {
	if !strings.HasPrefix(selectorArg, "@@") {
		return nil, false
	}
	tag := strings.TrimPrefix(selectorArg, "@@")
	if tag == "" || strings.ContainsAny(tag, ",;@") {
		return nil, false
	}
	r, err := provider.LoadInstances(p)
	if err != nil {
		return nil, false
	}
	var hits []provider.Instance
	for _, inst := range r.All() {
		if inst.HasTag(tag) {
			hits = append(hits, inst)
		}
	}
	if len(hits) == 0 {
		return nil, false
	}
	return hits, true
}

// fleetResult captures one instance's outcome inside a fan-out run.
type fleetResult struct {
	Instance string `json:"instance"`
	Provider string `json:"provider"`
	Exit     int    `json:"exit"`
	UpMs     int64  `json:"upMs"`
	RunMs    int64  `json:"runMs"`
	DownMs   int64  `json:"downMs"`
	Error    string `json:"error,omitempty"`
}

// runInstanceFleet runs `rest` (a flow file or argv command) against every
// instance in insts concurrently. Each instance gets its own lifecycle
// (Up → hooks → run → hooks → Down), its own evidence subdir under the
// shared run, and its own per-target log files. One aggregate Discord
// event fires at start, one at end — individual instances never spam.
func runInstanceFleet(cmd *cobra.Command, paths config.Paths, selectorArg string, insts []provider.Instance, rest []string, dryRun, asJSON, noEvidence, noNotify bool, maxParallel int, failFast, continueOnError bool, retries int) error {
	if len(insts) == 0 {
		return fmt.Errorf("no instances matched %s", selectorArg)
	}

	// Resolve the requested flow / cmd once so all instances see the same input.
	var loadedFlow *flow.Flow
	var cmdArgs []string
	if len(rest) == 1 && flow.LooksLikeFlowPath(rest[0]) {
		f, err := flow.Load(rest[0])
		if err != nil {
			return err
		}
		loadedFlow = f
	} else if len(rest) > 0 {
		cmdArgs = rest
	} else {
		return fmt.Errorf("run %s: missing flow or command", selectorArg)
	}

	if dryRun {
		return printFleetPlan(cmd.OutOrStdout(), selectorArg, insts, loadedFlow, joinArgs(cmdArgs), asJSON)
	}

	var run *evidence.Run
	if !noEvidence {
		var err error
		run, err = evidence.New(paths.RunsDir)
		if err != nil {
			return err
		}
		if loadedFlow != nil {
			run.SetFlow(loadedFlow.SourceFile)
		} else {
			run.SetCmd(joinArgs(cmdArgs))
		}
		run.SetSelector(selectorArg)
	}

	// One aggregate Notifier handle for the whole fleet. The per-instance
	// runner never builds its own — we mute them by passing a no-op handle.
	cmdLine := joinArgs(cmdArgs)
	if loadedFlow != nil {
		cmdLine = loadedFlow.SourceFile
	}
	fleetInst := provider.Instance{
		Name:     strings.TrimPrefix(selectorArg, "@@"),
		Provider: "fleet",
	}
	nfy := loadNotifier(cmd, paths, noNotify, fleetInst, selectorArg+fmt.Sprintf(" ×%d", len(insts)), cmdLine, run)
	nfy.Start()

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	results := make([]fleetResult, len(insts))
	exits := make([]int, len(insts))
	var failures atomic.Int32

	parallel := maxParallel
	if parallel <= 0 || parallel > len(insts) {
		parallel = len(insts)
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	fleetStart := time.Now()

	for i := range insts {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if failFast && failures.Load() > 0 && !continueOnError {
				results[i] = fleetResult{
					Instance: insts[i].Name,
					Provider: insts[i].Provider,
					Error:    "skipped: fail-fast triggered by earlier failure",
				}
				exits[i] = 1
				return
			}
			res := runOneFleetMember(ctx, cmd, paths, run, insts[i], loadedFlow, cmdArgs, retries)
			results[i] = res
			exits[i] = res.Exit
			if res.Exit != 0 || res.Error != "" {
				failures.Add(1)
				if failFast && !continueOnError {
					cancel()
				}
			}
		}()
	}
	wg.Wait()

	// Aggregate exit: any non-zero → 1.
	exit := 0
	for _, e := range exits {
		if e != 0 {
			exit = 1
			break
		}
	}
	totalMs := time.Since(fleetStart).Milliseconds()

	// Build evidence target summaries + finish the parent run.
	if run != nil {
		for i, r := range results {
			run.AddTarget(evidence.TargetSummary{
				Name:      insts[i].Name,
				Transport: insts[i].Target.Transport,
				ExitCode:  r.Exit,
				Duration:  r.RunMs,
				Error:     r.Error,
			})
		}
		meta, _ := run.Finish(exit)
		_, _ = run.WriteJUnit()
		fmt.Fprintf(cmd.ErrOrStderr(), "\nfleet-run-id: %s (%s) total=%dms (%d instances)\n",
			meta.ID, run.Dir, totalMs, len(insts))
	}

	// Aggregate Notify event. Compose stderrTail from each failing instance's
	// summary so the Discord failure message lists which ones broke.
	var errSummary strings.Builder
	for i, r := range results {
		if r.Exit == 0 && r.Error == "" {
			continue
		}
		if errSummary.Len() > 0 {
			errSummary.WriteByte('\n')
		}
		fmt.Fprintf(&errSummary, "%s: exit=%d %s", insts[i].Name, r.Exit, r.Error)
	}
	nfy.cmd = fmt.Sprintf("%s · %d ok / %d failed", cmdLine, len(insts)-int(failures.Load()), failures.Load())
	nfy.run = run
	nfy.FinishWithStderr(0, totalMs, 0, exit, firstError(results), errSummary.String())

	if asJSON {
		_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"selector": selectorArg,
			"results":  results,
			"exit":     exit,
			"totalMs":  totalMs,
		})
	}
	if exit != 0 && !continueOnError {
		os.Exit(exit)
	}
	return nil
}

// runOneFleetMember executes one instance's full lifecycle inside a fleet
// run. Output streams to per-instance log files under <run.Dir>/targets/<inst>/.
func runOneFleetMember(ctx context.Context, cmd *cobra.Command, paths config.Paths, parent *evidence.Run, inst provider.Instance, fl *flow.Flow, cmdArgs []string, retries int) fleetResult {
	r := fleetResult{Instance: inst.Name, Provider: inst.Provider}
	pr, err := provider.Default().Get(inst.Provider)
	if err != nil {
		r.Error = err.Error()
		r.Exit = 1
		return r
	}

	lock, err := acquireInstanceLockAt(cmd, paths, inst.Name)
	if err != nil {
		r.Error = err.Error()
		r.Exit = 1
		return r
	}
	defer lock.Release()

	// Per-instance log writers, prefixed with [inst] so concurrent output is
	// readable on the terminal.
	prefOut := &prefixWriter{w: cmd.OutOrStdout(), prefix: "[" + inst.Name + "] "}
	prefErr := &prefixWriter{w: cmd.ErrOrStderr(), prefix: "[" + inst.Name + "] "}
	var teeOut, teeErr io.Writer = prefOut, prefErr
	var logs *evidence.TargetLogs
	if parent != nil {
		o, e, l, lerr := parent.TargetWriters(inst.Name, prefOut, prefErr)
		if lerr == nil {
			teeOut, teeErr, logs = o, e, l
			defer logs.Close()
		}
	}

	// pre_up hooks (no transport yet).
	if err := (&hooks.Runner{Stdout: teeOut, Stderr: teeErr}).Run(ctx, hooks.PhasePreUp, inst.Hooks.PreUp); err != nil {
		r.Error = err.Error()
		r.Exit = 1
		return r
	}

	upStart := time.Now()
	tgt, ensure, upErr := pr.Up(ctx, inst)
	r.UpMs = time.Since(upStart).Milliseconds()
	if upErr != nil {
		r.Error = upErr.Error()
		r.Exit = 1
		return r
	}

	onSuccess, _ := provider.ParseDispose(inst.Disp.OnSuccess)
	onFailure, _ := provider.ParseDispose(inst.Disp.OnFailure)
	if onFailure == provider.DisposeKeep {
		onFailure = onSuccess
	}
	only := inst.Disp.OnlyIfWeStarted

	reg := transport.Default()
	tr, err := reg.Get(tgt.Transport)
	if err != nil {
		downStart := time.Now()
		_ = pr.Down(context.Background(), inst, restoreOnDisposable(only, ensure.Changed, onFailure))
		r.DownMs = time.Since(downStart).Milliseconds()
		r.Error = err.Error()
		r.Exit = 1
		return r
	}

	// post_up hooks (transport ready).
	if err := (&hooks.Runner{Transport: tr, Target: tgt, Stdout: teeOut, Stderr: teeErr}).Run(ctx, hooks.PhasePostUp, inst.Hooks.PostUp); err != nil {
		downStart := time.Now()
		_ = pr.Down(context.Background(), inst, restoreOnDisposable(only, ensure.Changed, onFailure))
		r.DownMs = time.Since(downStart).Milliseconds()
		r.Error = err.Error()
		r.Exit = 1
		return r
	}

	runStart := time.Now()
	exit := 0
	var runErr error
	for attempt := 0; attempt <= retries; attempt++ {
		exit = 0
		runErr = nil
		if fl != nil {
			steps, ferr := flow.Run(ctx, tr, tgt, fl, teeOut, teeErr)
			runErr = ferr
			exit = lastExit(steps, ferr)
			if parent != nil {
				_, _ = parent.WriteSteps(inst.Name, steps)
			}
		} else {
			res, rerr := tr.Run(ctx, tgt, cmdArgs, teeOut, teeErr)
			runErr = rerr
			exit = res.ExitCode
			if rerr != nil && exit == 0 {
				exit = 1
			}
		}
		if exit == 0 && runErr == nil {
			break
		}
	}
	r.RunMs = time.Since(runStart).Milliseconds()

	// pre_down hooks (best-effort).
	_ = (&hooks.Runner{Transport: tr, Target: tgt, Stdout: teeOut, Stderr: teeErr}).Run(ctx, hooks.PhasePreDown, inst.Hooks.PreDown)

	want := onSuccess
	if exit != 0 || runErr != nil {
		want = onFailure
	}
	want = restoreOnDisposable(only, ensure.Changed, want)
	downStart := time.Now()
	if want != provider.DisposeKeep {
		_ = pr.Down(context.Background(), inst, want)
	}
	r.DownMs = time.Since(downStart).Milliseconds()

	r.Exit = exit
	if runErr != nil {
		r.Error = runErr.Error()
		if r.Exit == 0 {
			r.Exit = 1
		}
	}
	return r
}

// restoreOnDisposable applies the only_if_we_started constraint: if we
// didn't change the prior state, leave the instance alone.
func restoreOnDisposable(only, changed bool, want provider.Dispose) provider.Dispose {
	if only && !changed {
		return provider.DisposeKeep
	}
	return want
}

func firstError(rs []fleetResult) error {
	for _, r := range rs {
		if r.Error != "" {
			return fmt.Errorf("%s: %s", r.Instance, r.Error)
		}
		if r.Exit != 0 {
			return fmt.Errorf("%s: exit=%d", r.Instance, r.Exit)
		}
	}
	return nil
}

// prefixWriter prefixes every newline-delimited write with `prefix`, used
// to disambiguate concurrent fleet output on the terminal.
type prefixWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
	atSOL  bool
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	out := make([]byte, 0, len(b)+len(p.prefix)*4)
	for i, c := range b {
		if i == 0 && p.atSOL || i == 0 && len(out) == 0 && len(b) > 0 {
			out = append(out, p.prefix...)
		}
		out = append(out, c)
		if c == '\n' && i < len(b)-1 {
			out = append(out, p.prefix...)
		}
		if c == '\n' {
			p.atSOL = true
		} else {
			p.atSOL = false
		}
	}
	_, err := p.w.Write(out)
	return len(b), err
}

func printFleetPlan(w io.Writer, selector string, insts []provider.Instance, f *flow.Flow, cmdLine string, asJSON bool) error {
	plan := map[string]any{
		"selector":  selector,
		"instances": instanceNames(insts),
	}
	if f != nil {
		plan["flow"] = f.SourceFile
	} else {
		plan["command"] = cmdLine
	}
	if asJSON {
		return json.NewEncoder(w).Encode(plan)
	}
	fmt.Fprintf(w, "dry-run plan: %s × %d instances\n", selector, len(insts))
	for _, i := range insts {
		fmt.Fprintf(w, "  %s (provider=%s, tags=%s)\n", i.Name, i.Provider, strings.Join(i.Tags, ","))
	}
	if f != nil {
		fmt.Fprintf(w, "flow:    %s\n", f.SourceFile)
	} else {
		fmt.Fprintf(w, "command: %s\n", cmdLine)
	}
	_ = filepath.Separator
	return nil
}

func instanceNames(insts []provider.Instance) []string {
	out := make([]string, len(insts))
	for i, in := range insts {
		out[i] = in.Name
	}
	return out
}
