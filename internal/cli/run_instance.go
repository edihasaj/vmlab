package cli

import (
	"context"
	"encoding/json"
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
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

// instanceShortcut returns the instance name when selectorArg is a single
// `@<name>` atom that matches a configured instance. Anything else returns
// ok=false so the caller falls back to the existing target-selector path.
func instanceShortcut(selectorArg string, p config.Paths) (string, bool) {
	if !strings.HasPrefix(selectorArg, "@") {
		return "", false
	}
	rest := strings.TrimPrefix(selectorArg, "@")
	if rest == "" || strings.ContainsAny(rest, ",;") {
		return "", false
	}
	r, err := provider.LoadInstances(p)
	if err != nil {
		return "", false
	}
	if _, ok := r.Get(rest); !ok {
		return "", false
	}
	return rest, true
}

// runInstance handles `vmlab run @<instance> <flow-or-cmd>`. It brings the
// instance up, runs the flow or command against the emitted target, then
// disposes per the instance's disposition. Always single-target — to fan
// out across instances use a target tag instead.
func runInstance(cmd *cobra.Command, paths config.Paths, name string, rest []string, dryRun, asJSON, noEvidence, noNotify bool, retries int) error {
	pr, inst, err := resolveInstance(name)
	if err != nil {
		return err
	}
	if !dryRun {
		lock, err := acquireInstanceLockAt(cmd, paths, inst.Name)
		if err != nil {
			return err
		}
		defer lock.Release()
	}

	var loadedFlow *flow.Flow
	var cmdArgs []string
	if len(rest) == 1 && flow.LooksLikeFlowPath(rest[0]) {
		loadedFlow, err = flow.Load(rest[0])
		if err != nil {
			return err
		}
	} else if len(rest) > 0 {
		cmdArgs = rest
	} else {
		return fmt.Errorf("run @%s: missing flow or command", name)
	}

	if dryRun {
		return printInstancePlan(cmd.OutOrStdout(), inst, loadedFlow, joinArgs(cmdArgs), asJSON)
	}

	var run *evidence.Run
	if !noEvidence {
		run, err = evidence.New(paths.RunsDir)
		if err != nil {
			return err
		}
		if loadedFlow != nil {
			run.SetFlow(loadedFlow.SourceFile)
		} else {
			run.SetCmd(joinArgs(cmdArgs))
		}
		run.SetSelector("@" + inst.Name)
	}

	cmdline := joinArgs(cmdArgs)
	if loadedFlow != nil {
		cmdline = loadedFlow.SourceFile
	}
	nfy := loadNotifier(cmd, paths, noNotify, inst, "@"+inst.Name, cmdline, run)
	nfy.Start()

	if err := runLifecycleHooks(cmd, run, hooks.PhasePreUp, inst.Hooks.PreUp, nil, target.Target{}); err != nil {
		finishLifecycle(run, inst, provider.EnsureResult{}, "", 0, 0, 0, err)
		nfy.Finish(0, 0, 0, 1, err)
		if run != nil {
			run.Finish(1)
		}
		return err
	}

	upStart := time.Now()
	tgt, ensure, upErr := pr.Up(cmd.Context(), inst)
	upMs := time.Since(upStart).Milliseconds()
	if run != nil {
		_ = run.WriteFile("status-before.txt", []byte(ensure.PriorState.String()+"\n"))
	}
	if upErr != nil {
		finishLifecycle(run, inst, ensure, "", upMs, 0, 0, upErr)
		nfy.Finish(upMs, 0, 0, 1, upErr)
		if run != nil {
			run.Finish(1)
		}
		return upErr
	}

	onSuccess, err := provider.ParseDispose(inst.Disp.OnSuccess)
	if err != nil {
		onSuccess = provider.DisposeSuspend
	}
	onFailure, err := provider.ParseDispose(inst.Disp.OnFailure)
	if err != nil {
		onFailure = onSuccess
	}
	only := inst.Disp.OnlyIfWeStarted

	reg := transport.Default()
	tr, err := reg.Get(tgt.Transport)
	if err != nil {
		downMs, _ := cleanupInstance(cmd, run, pr, inst, ensure, only, onFailure, upMs, 0, err)
		if run != nil {
			run.Finish(1)
		}
		nfy.Finish(upMs, 0, downMs, 1, err)
		return err
	}

	var teeOut, teeErr io.Writer = cmd.OutOrStdout(), cmd.ErrOrStderr()
	var logs *evidence.TargetLogs
	if run != nil {
		o, e, l, lerr := run.TargetWriters(inst.Name, cmd.OutOrStdout(), cmd.ErrOrStderr())
		if lerr != nil {
			return lerr
		}
		teeOut, teeErr, logs = o, e, l
		defer logs.Close()
	}

	if err := runLifecycleHooks(cmd, run, hooks.PhasePostUp, inst.Hooks.PostUp, tr, tgt); err != nil {
		downMs, _ := cleanupInstance(cmd, run, pr, inst, ensure, only, onFailure, upMs, 0, err)
		if logs != nil {
			_ = logs.Close()
		}
		if run != nil {
			run.Finish(1)
		}
		nfy.Finish(upMs, 0, downMs, 1, err)
		return err
	}

	runStart := time.Now()
	exit := 0
	var runErr error
	attempts := 0
	for attempt := 0; attempt <= retries; attempt++ {
		attempts = attempt + 1
		if attempt > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "\nrun @%s: retry %d/%d after exit=%d\n", inst.Name, attempt, retries, exit)
		}
		exit = 0
		runErr = nil
		if loadedFlow != nil {
			steps, ferr := flow.Run(cmd.Context(), tr, tgt, loadedFlow, teeOut, teeErr)
			runErr = ferr
			exit = lastExit(steps, ferr)
			if run != nil {
				_, _ = run.WriteSteps(inst.Name, steps)
			}
		} else {
			res, rerr := tr.Run(cmd.Context(), tgt, cmdArgs, teeOut, teeErr)
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
	runMs := time.Since(runStart).Milliseconds()
	if run != nil && attempts > 1 {
		_ = run.WriteFile("attempts.txt", []byte(fmt.Sprintf("%d\n", attempts)))
	}

	if hookErr := runLifecycleHooks(cmd, run, hooks.PhasePreDown, inst.Hooks.PreDown, tr, tgt); hookErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "run @%s: pre_down hooks failed: %v\n", inst.Name, hookErr)
		if runErr == nil {
			runErr = hookErr
			if exit == 0 {
				exit = 1
			}
		}
	}

	want := onSuccess
	if exit != 0 || runErr != nil {
		want = onFailure
	}
	downMs, _ := cleanupInstance(cmd, run, pr, inst, ensure, only, want, upMs, runMs, runErr)

	if logs != nil {
		_ = logs.Close()
	}
	if run != nil {
		run.AddTarget(evidence.TargetSummary{
			Name:      inst.Name,
			Transport: tgt.Transport,
			ExitCode:  exit,
			Duration:  runMs,
			Error:     errString(runErr),
		})
		meta, _ := run.Finish(exit)
		_, _ = run.WriteJUnit()
		fmt.Fprintf(cmd.ErrOrStderr(), "\nrun-id: %s (%s) up=%dms run=%dms down=%dms\n",
			meta.ID, run.Dir, upMs, runMs, downMs)
	}
	nfy.Finish(upMs, runMs, downMs, exit, runErr)

	if asJSON {
		_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"instance": inst.Name,
			"provider": inst.Provider,
			"exit":     exit,
			"upMs":     upMs,
			"runMs":    runMs,
			"downMs":   downMs,
		})
	}

	if runErr != nil {
		return runErr
	}
	if exit != 0 {
		os.Exit(exit)
	}
	return nil
}

// cleanupInstance is the @instance equivalent of cleanupAndFinish — see
// lifecycle.go. Kept separate so each command owns its own evidence path.
func cleanupInstance(cmd *cobra.Command, run *evidence.Run, pr provider.Provider, inst provider.Instance, ensure provider.EnsureResult, only bool, want provider.Dispose, upMs, runMs int64, runErr error) (int64, error) {
	if only && !ensure.Changed {
		fmt.Fprintf(cmd.ErrOrStderr(), "run @%s: leaving instance as-is (was already running)\n", inst.Name)
		want = provider.DisposeKeep
	}
	downStart := time.Now()
	var downErr error
	if want != provider.DisposeKeep {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		downErr = pr.Down(ctx, inst, want)
		cancel()
		if downErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "run @%s: cleanup (%s) failed: %v\n", inst.Name, want, downErr)
		}
	}
	downMs := time.Since(downStart).Milliseconds()
	if run != nil {
		postCtx, postCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if st, sErr := pr.Status(postCtx, inst); sErr == nil {
			_ = run.WriteFile("status-after.txt", []byte(st.String()+"\n"))
		}
		postCancel()
	}
	finishLifecycle(run, inst, ensure, want.String(), upMs, runMs, downMs, runErr)
	return downMs, downErr
}

func printInstancePlan(w io.Writer, inst provider.Instance, f *flow.Flow, cmdLine string, asJSON bool) error {
	plan := map[string]any{
		"instance": inst.Name,
		"provider": inst.Provider,
	}
	if f != nil {
		plan["flow"] = f.SourceFile
		steps := make([]map[string]any, 0, len(f.Steps))
		for i, s := range f.Steps {
			kind, line := "run", s.Run
			if s.Assert != "" {
				kind, line = "assert", s.Assert
			}
			steps = append(steps, map[string]any{"index": i, "kind": kind, "cmd": line})
		}
		plan["steps"] = steps
	} else {
		plan["command"] = cmdLine
	}
	plan["disposition"] = map[string]any{
		"on_success":         inst.Disp.OnSuccess,
		"on_failure":         inst.Disp.OnFailure,
		"only_if_we_started": inst.Disp.OnlyIfWeStarted,
	}
	if asJSON {
		return json.NewEncoder(w).Encode(plan)
	}
	fmt.Fprintf(w, "dry-run plan: @%s (provider=%s)\n", inst.Name, inst.Provider)
	if f != nil {
		fmt.Fprintf(w, "flow:    %s\n", f.SourceFile)
		for i, s := range f.Steps {
			kind, line := "run", s.Run
			if s.Assert != "" {
				kind, line = "assert", s.Assert
			}
			fmt.Fprintf(w, "  %d. %-6s %s\n", i, kind, line)
		}
	} else {
		fmt.Fprintf(w, "command: %s\n", cmdLine)
	}
	fmt.Fprintf(w, "disposition: on_success=%s on_failure=%s only_if_we_started=%v\n",
		inst.Disp.OnSuccess, inst.Disp.OnFailure, inst.Disp.OnlyIfWeStarted)
	return nil
}
