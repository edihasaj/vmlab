package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/fleet"
	"github.com/edihasaj/vmlab/internal/flow"
	"github.com/edihasaj/vmlab/internal/provider"
	_ "github.com/edihasaj/vmlab/internal/provider/all"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// registerTools wires every vmlab MCP tool into the server. Read-only tools
// are always available; write tools require allowWrite.
func registerTools(s *mcpserver.MCPServer, allowWrite bool) {
	s.AddTool(
		mcpgo.NewTool("vmlab_targets",
			mcpgo.WithDescription("List configured targets with their tags and transports.")),
		handleTargets,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_doctor",
			mcpgo.WithDescription("Check transport binaries and target reachability."),
			mcpgo.WithString("selector", mcpgo.Description("Target selector (default: all)"))),
		handleDoctor,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_evidence",
			mcpgo.WithDescription("List recent run summaries (read-only)."),
			mcpgo.WithNumber("limit", mcpgo.Description("Max number of runs to return"))),
		handleEvidence,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_instances",
			mcpgo.WithDescription("List configured provider instances (read-only).")),
		handleInstances,
	)

	if !allowWrite {
		return
	}

	s.AddTool(
		mcpgo.NewTool("vmlab_up",
			mcpgo.WithDescription("Bring a provider instance to running (idempotent)."),
			mcpgo.WithString("instance", mcpgo.Required(), mcpgo.Description("Instance name"))),
		handleUp,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_down",
			mcpgo.WithDescription("Dispose of a provider instance (idempotent)."),
			mcpgo.WithString("instance", mcpgo.Required(), mcpgo.Description("Instance name")),
			mcpgo.WithString("dispose", mcpgo.Description("keep|suspend|poweroff|destroy"))),
		handleDown,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_with",
			mcpgo.WithDescription("Bring an instance up, run a command, restore prior state."),
			mcpgo.WithString("instance", mcpgo.Required(), mcpgo.Description("Instance name")),
			mcpgo.WithArray("command", mcpgo.Required(),
				mcpgo.Items(map[string]any{"type": "string"})),
			mcpgo.WithString("dispose", mcpgo.Description("Override disposition on success"))),
		handleWith,
	)

	s.AddTool(
		mcpgo.NewTool("vmlab_run",
			mcpgo.WithDescription("Run a shell command or YAML flow against a target selector."),
			mcpgo.WithString("selector", mcpgo.Required(), mcpgo.Description("Target selector")),
			mcpgo.WithString("command", mcpgo.Description("Shell command (mutually exclusive with flowPath)")),
			mcpgo.WithString("flowPath", mcpgo.Description("Path to a flow YAML")),
			mcpgo.WithNumber("maxParallel", mcpgo.Description("Max concurrent targets")),
			mcpgo.WithBoolean("failFast", mcpgo.Description("Stop launching new work after first failure"))),
		handleRun,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_web",
			mcpgo.WithDescription("Run an abx-style web action against a web target."),
			mcpgo.WithString("target", mcpgo.Required()),
			mcpgo.WithArray("args", mcpgo.Required(),
				mcpgo.Items(map[string]any{"type": "string"}))),
		handleWeb,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_gui",
			mcpgo.WithDescription("Run a guiport-style desktop action against a gui target."),
			mcpgo.WithString("target", mcpgo.Required()),
			mcpgo.WithString("kind", mcpgo.Required(),
				mcpgo.Enum("click", "type", "screenshot", "run")),
			mcpgo.WithString("selector"),
			mcpgo.WithString("text"),
			mcpgo.WithString("path")),
		handleGUI,
	)
}

// ---- handlers ---------------------------------------------------------------

func handleTargets(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	_, p, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	r, err := target.Load(p)
	if err != nil {
		return helperError(err.Error()), nil
	}
	return helperResult(mustJSON(r.All())), nil
}

func handleDoctor(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	sel := stringArg(req, "selector", "all")
	_, paths, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	r, err := target.Load(paths)
	if err != nil {
		return helperError(err.Error()), nil
	}
	ts, err := target.NewSelector(sel).Resolve(r)
	if err != nil {
		return helperError(err.Error()), nil
	}
	reg := transport.Default()
	rows := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		tr, err := reg.Get(t.Transport)
		if err != nil {
			rows = append(rows, map[string]any{"name": t.Name, "ok": false, "message": err.Error()})
			continue
		}
		h := tr.Doctor(ctx, t)
		rows = append(rows, map[string]any{"name": t.Name, "transport": t.Transport, "ok": h.OK, "message": h.Message})
	}
	return helperResult(mustJSON(rows)), nil
}

func handleEvidence(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	limit := intArg(req, "limit", 20)
	cfg, _, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	runs, err := evidence.List(cfg.RunsDir)
	if err != nil {
		return helperError(err.Error()), nil
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return helperResult(mustJSON(runs)), nil
}

func handleRun(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	sel := stringArg(req, "selector", "")
	cmdLine := stringArg(req, "command", "")
	flowPath := stringArg(req, "flowPath", "")
	maxParallel := intArg(req, "maxParallel", 0)
	failFast := boolArg(req, "failFast", false)

	if sel == "" {
		return helperError("selector is required"), nil
	}
	if (cmdLine == "") == (flowPath == "") {
		return helperError("provide exactly one of command or flowPath"), nil
	}

	_, paths, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	if err := config.EnsureDirs(paths); err != nil {
		return helperError(err.Error()), nil
	}
	r, err := target.Load(paths)
	if err != nil {
		return helperError(err.Error()), nil
	}
	ts, err := target.NewSelector(sel).Resolve(r)
	if err != nil {
		return helperError(err.Error()), nil
	}
	reg := transport.Default()
	var f *flow.Flow
	if flowPath != "" {
		f, err = flow.Load(flowPath)
		if err != nil {
			return helperError(err.Error()), nil
		}
	}
	run, err := evidence.New(paths.RunsDir)
	if err != nil {
		return helperError(err.Error()), nil
	}
	if f != nil {
		run.SetFlow(f.SourceFile)
	} else {
		run.SetCmd(cmdLine)
	}
	run.SetSelector(sel)

	var stdout, stderr bytes.Buffer
	outcomes, runErr := fleet.Run(ctx, ts, reg,
		fleet.Options{MaxParallel: maxParallel, FailFast: failFast},
		&stdout, &stderr,
		func(ctx context.Context, t target.Target, tr transport.Transport, so, se io.Writer) (int, error) {
			outW, errW, logs, err := run.TargetWriters(t.Name, &stdout, &stderr)
			if err != nil {
				return 0, err
			}
			defer logs.Close()
			if f != nil {
				steps, err := flow.Run(ctx, tr, t, f, outW, errW)
				_, _ = run.WriteSteps(t.Name, steps)
				if err != nil {
					return lastFlowExit(steps, err), err
				}
				return 0, nil
			}
			res, err := tr.Run(ctx, t, transport.WrapShell(t, cmdLine), outW, errW)
			return res.ExitCode, err
		})
	exit := fleet.AggregateExit(outcomes)
	for _, o := range outcomes {
		ts := evidence.TargetSummary{Name: o.Target.Name, Transport: o.Target.Transport, ExitCode: o.ExitCode, Duration: o.Duration.Milliseconds()}
		if o.Error != nil {
			ts.Error = o.Error.Error()
		}
		run.AddTarget(ts)
	}
	meta, _ := run.Finish(exit)
	_, _ = run.WriteJUnit()
	return helperResult(mustJSON(map[string]any{
		"runId":  meta.ID,
		"exit":   exit,
		"meta":   meta,
		"err":    errOrNil(runErr),
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	})), nil
}

func handleWeb(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name := stringArg(req, "target", "")
	args := stringArrayArg(req, "args")
	if name == "" {
		return helperError("target is required"), nil
	}
	_, paths, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	r, err := target.Load(paths)
	if err != nil {
		return helperError(err.Error()), nil
	}
	t, ok := r.Get(name)
	if !ok {
		return helperError(fmt.Sprintf("unknown target: %s", name)), nil
	}
	tr, err := transport.Default().Get(t.Transport)
	if err != nil {
		return helperError(err.Error()), nil
	}
	var stdout, stderr bytes.Buffer
	res, err := tr.Run(ctx, t, args, &stdout, &stderr)
	if err != nil {
		return helperError(err.Error()), nil
	}
	return helperResult(mustJSON(map[string]any{"exit": res.ExitCode, "stdout": stdout.String(), "stderr": stderr.String()})), nil
}

func handleInstances(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	_, p, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	r, err := provider.LoadInstances(p)
	if err != nil {
		return helperError(err.Error()), nil
	}
	return helperResult(mustJSON(r.All())), nil
}

func handleUp(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name := stringArg(req, "instance", "")
	pr, inst, err := resolveInstanceForMCP(name)
	if err != nil {
		return helperError(err.Error()), nil
	}
	tgt, res, err := pr.Up(ctx, inst)
	if err != nil {
		return helperError(err.Error()), nil
	}
	return helperResult(mustJSON(map[string]any{
		"instance":   inst.Name,
		"target":     tgt.Name,
		"transport":  tgt.Transport,
		"changed":    res.Changed,
		"priorState": res.PriorState.String(),
		"reason":     res.Reason,
	})), nil
}

func handleDown(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name := stringArg(req, "instance", "")
	disposeRaw := stringArg(req, "dispose", "")
	pr, inst, err := resolveInstanceForMCP(name)
	if err != nil {
		return helperError(err.Error()), nil
	}
	d, err := resolveDisposeMCP(disposeRaw, inst.Disp.OnSuccess, provider.DisposeSuspend)
	if err != nil {
		return helperError(err.Error()), nil
	}
	if err := pr.Down(ctx, inst, d); err != nil {
		return helperError(err.Error()), nil
	}
	return helperResult(mustJSON(map[string]any{"instance": inst.Name, "dispose": d.String()})), nil
}

func handleWith(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name := stringArg(req, "instance", "")
	command := stringArrayArg(req, "command")
	disposeRaw := stringArg(req, "dispose", "")
	if len(command) == 0 {
		return helperError("command is required"), nil
	}
	pr, inst, err := resolveInstanceForMCP(name)
	if err != nil {
		return helperError(err.Error()), nil
	}
	tgt, res, err := pr.Up(ctx, inst)
	if err != nil {
		return helperError(err.Error()), nil
	}
	onSuccess, err := resolveDisposeMCP(disposeRaw, inst.Disp.OnSuccess, provider.DisposeSuspend)
	if err != nil {
		return helperError(err.Error()), nil
	}
	onFailure, err := resolveDisposeMCP(disposeRaw, inst.Disp.OnFailure, onSuccess)
	if err != nil {
		return helperError(err.Error()), nil
	}
	tr, err := transport.Default().Get(tgt.Transport)
	if err != nil {
		return helperError(err.Error()), nil
	}
	var stdout, stderr bytes.Buffer
	runRes, runErr := tr.Run(ctx, tgt, command, &stdout, &stderr)
	// cleanup honours disposition.only_if_we_started
	if !inst.Disp.OnlyIfWeStarted || res.Changed {
		want := onSuccess
		if runErr != nil || runRes.ExitCode != 0 {
			want = onFailure
		}
		if cErr := pr.Down(ctx, inst, want); cErr != nil {
			stderr.WriteString("cleanup: " + cErr.Error() + "\n")
		}
	}
	out := map[string]any{
		"instance": inst.Name,
		"exit":     runRes.ExitCode,
		"stdout":   stdout.String(),
		"stderr":   stderr.String(),
		"changed":  res.Changed,
	}
	if runErr != nil {
		out["err"] = runErr.Error()
	}
	return helperResult(mustJSON(out)), nil
}

func resolveInstanceForMCP(name string) (provider.Provider, provider.Instance, error) {
	if name == "" {
		return nil, provider.Instance{}, fmt.Errorf("instance is required")
	}
	_, p, err := config.Load()
	if err != nil {
		return nil, provider.Instance{}, err
	}
	r, err := provider.LoadInstances(p)
	if err != nil {
		return nil, provider.Instance{}, err
	}
	inst, ok := r.Get(name)
	if !ok {
		return nil, provider.Instance{}, fmt.Errorf("unknown instance: %s", name)
	}
	pr, err := provider.Default().Get(inst.Provider)
	if err != nil {
		return nil, inst, err
	}
	return pr, inst, nil
}

func resolveDisposeMCP(flag, fromInstance string, fallback provider.Dispose) (provider.Dispose, error) {
	if flag != "" {
		return provider.ParseDispose(flag)
	}
	if fromInstance != "" {
		return provider.ParseDispose(fromInstance)
	}
	return fallback, nil
}

func handleGUI(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name := stringArg(req, "target", "")
	kind := stringArg(req, "kind", "")
	if name == "" || kind == "" {
		return helperError("target and kind are required"), nil
	}
	_, paths, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	r, err := target.Load(paths)
	if err != nil {
		return helperError(err.Error()), nil
	}
	t, ok := r.Get(name)
	if !ok {
		return helperError(fmt.Sprintf("unknown target: %s", name)), nil
	}
	tr, err := transport.Default().Get(t.Transport)
	if err != nil {
		return helperError(err.Error()), nil
	}
	action := transport.GUIAction{
		Kind:     kind,
		Selector: stringArg(req, "selector", ""),
		Text:     stringArg(req, "text", ""),
		Path:     stringArg(req, "path", ""),
	}
	if err := tr.GUI(ctx, t, action); err != nil {
		return helperError(err.Error()), nil
	}
	return helperResult(mustJSON(map[string]any{"ok": true})), nil
}

// ---- helpers ---------------------------------------------------------------

func stringArg(req mcpgo.CallToolRequest, name, def string) string {
	args := req.GetArguments()
	if v, ok := args[name]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func intArg(req mcpgo.CallToolRequest, name string, def int) int {
	args := req.GetArguments()
	v, ok := args[name]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	}
	return def
}

func boolArg(req mcpgo.CallToolRequest, name string, def bool) bool {
	args := req.GetArguments()
	if v, ok := args[name]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func stringArrayArg(req mcpgo.CallToolRequest, name string) []string {
	args := req.GetArguments()
	v, ok := args[name]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(b)
}

func errOrNil(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func lastFlowExit(steps []flow.StepResult, err error) int {
	if len(steps) > 0 {
		last := steps[len(steps)-1]
		if last.ExitCode != 0 {
			return last.ExitCode
		}
	}
	if err != nil {
		return 1
	}
	return 0
}
