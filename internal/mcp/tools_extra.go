package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/edihasaj/vmlab/internal/provider"
	_ "github.com/edihasaj/vmlab/internal/provider/all"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// registerExtraTools wires the v0.2+ tool surface so an agent can drive the
// full goal scenario over MCP: spin instances, fan out flows across @@<tag>,
// bake images, report cost, sweep orphans, cancel stuck runs.
//
// Tools that need the lifecycle wrapper (Up → hooks → run → Down → Notify)
// shell out to the same vmlab binary instead of duplicating the orchestration
// logic. Read-only / lightweight tools (usage, orphans, cancel) run inline.
func registerExtraTools(s *mcpserver.MCPServer, opts Options) {
	// Read-only: usage + orphans list.
	s.AddTool(
		mcpgo.NewTool("vmlab_usage",
			mcpgo.WithDescription("Summarise lifecycle uptime across recent runs (provider × instance)."),
			mcpgo.WithString("groupBy", mcpgo.Description("instance | provider")),
			mcpgo.WithString("since", mcpgo.Description("only count runs newer than this Go duration (e.g. 24h)"))),
		handleUsage,
	)
	s.AddTool(
		mcpgo.NewTool("vmlab_orphans",
			mcpgo.WithDescription("List vmlab-tagged resources across registered cloud providers."),
			mcpgo.WithArray("providers",
				mcpgo.Description("Limit to a subset (default: all registered)"),
				mcpgo.Items(map[string]any{"type": "string"}))),
		handleOrphansList,
	)

	if opts.allows("vmlab_fleet_run") {
		s.AddTool(
			mcpgo.NewTool("vmlab_fleet_run",
				mcpgo.WithDescription("Run a flow or command across every instance matching @@<tag> in parallel. Full per-instance lifecycle (Up → hooks → run → Down) + aggregate Discord summary if configured. Returns the run id + per-instance exits."),
				mcpgo.WithString("tag", mcpgo.Required(), mcpgo.Description("Instance tag (the part after @@)")),
				mcpgo.WithString("flowPath", mcpgo.Description("Path to a flow YAML")),
				mcpgo.WithString("command", mcpgo.Description("Shell command (mutually exclusive with flowPath)")),
				mcpgo.WithNumber("maxParallel", mcpgo.Description("Max concurrent instances")),
				mcpgo.WithBoolean("failFast", mcpgo.Description("Cancel remaining instances on first failure")),
				mcpgo.WithNumber("retries", mcpgo.Description("Per-instance inner-run retries on failure"))),
			handleFleetRun,
		)
	}
	if opts.allows("vmlab_image_build") {
		s.AddTool(
			mcpgo.NewTool("vmlab_image_build",
				mcpgo.WithDescription("Bring up @<instance>, run the flow, snapshot it via the provider, then destroy. Future Up calls can start from the named image."),
				mcpgo.WithString("instance", mcpgo.Required(), mcpgo.Description("Source instance name")),
				mcpgo.WithString("flowPath", mcpgo.Required(), mcpgo.Description("Flow YAML to run before the snapshot")),
				mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Image name (used to look up the snapshot later)")),
				mcpgo.WithString("description", mcpgo.Description("Optional human-readable description")),
				mcpgo.WithBoolean("keep", mcpgo.Description("Leave source instance running after snapshot"))),
			handleImageBuild,
		)
	}
	if opts.allows("vmlab_notify_test") {
		s.AddTool(
			mcpgo.NewTool("vmlab_notify_test",
				mcpgo.WithDescription("Post a synthetic start/success/failure trio to every configured notifier — useful from agents that just edited ~/.vmlab/config.yaml to verify Discord wiring."),
				mcpgo.WithString("phase", mcpgo.Description("start | success | failure | all (default all)"))),
			handleNotifyTest,
		)
	}
	if opts.allows("vmlab_cancel") {
		s.AddTool(
			mcpgo.NewTool("vmlab_cancel",
				mcpgo.WithDescription("Signal a running run (SIGINT by default) so its cleanup hooks fire."),
				mcpgo.WithString("runId", mcpgo.Required()),
				mcpgo.WithString("signal", mcpgo.Description("INT | TERM | KILL (default INT)"))),
			handleCancel,
		)
	}
	if opts.allows("vmlab_orphans_destroy") {
		s.AddTool(
			mcpgo.NewTool("vmlab_orphans_destroy",
				mcpgo.WithDescription("Destroy every vmlab-tagged orphan across the requested providers. Cost safety net."),
				mcpgo.WithArray("providers",
					mcpgo.Description("Limit to a subset (default: all registered)"),
					mcpgo.Items(map[string]any{"type": "string"}))),
			handleOrphansDestroy,
		)
	}
	if opts.allows("vmlab_grant") {
		s.AddTool(
			mcpgo.NewTool("vmlab_grant",
				mcpgo.WithDescription("Open the macOS Privacy & Security pane for a TCC scope and poll until the binary reports the grant is in place. With auto=true, guiport navigates to the row so the human only Touch IDs. Returns a `needsHumanTouchID: true` field so the agent can surface a one-line prompt to the user."),
				mcpgo.WithString("binary", mcpgo.Required(), mcpgo.Description("Binary to grant — usually 'guiport' or 'um'")),
				mcpgo.WithString("scope", mcpgo.Description("screen-recording | accessibility | input-monitoring | full-disk-access | automation | camera | microphone (default: screen-recording)")),
				mcpgo.WithBoolean("auto", mcpgo.Description("Drive the pane via guiport (requires Accessibility already granted to guiport)")),
				mcpgo.WithString("timeout", mcpgo.Description("Max wait for the grant to land (Go duration, default 5m)")),
				mcpgo.WithBoolean("noWait", mcpgo.Description("Just open the pane and return — don't poll"))),
			handleGrant,
		)
	}
	if opts.allows("vmlab_matrix_run") {
		s.AddTool(
			mcpgo.NewTool("vmlab_matrix_run",
				mcpgo.WithDescription("Run a flow or command against any vmlab selector and return one compact ND-JSON row per target/instance. Failing rows include a 40-line stderr tail. This is the agent-friendly path: ~50× fewer tokens than vmlab_fleet_run for the same scenario."),
				mcpgo.WithString("selector", mcpgo.Required(), mcpgo.Description("@<inst> | @@<tag> | <target> | all")),
				mcpgo.WithString("flowPath", mcpgo.Description("Path to a flow YAML")),
				mcpgo.WithString("command", mcpgo.Description("Shell command (mutually exclusive with flowPath)")),
				mcpgo.WithString("fromSnapshot", mcpgo.Description("Restore named snapshot before Up (only for @<inst> / @@<tag>)")),
				mcpgo.WithNumber("retries", mcpgo.Description("Per-target inner-run retries on failure"))),
			handleMatrixRun,
		)
	}
}

// ---- usage ---------------------------------------------------------------

func handleUsage(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	groupBy := stringArg(req, "groupBy", "instance")
	sinceStr := stringArg(req, "since", "")

	cfg, _, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	runs, err := evidence.List(cfg.RunsDir)
	if err != nil {
		return helperError(err.Error()), nil
	}
	cutoff := time.Time{}
	if sinceStr != "" {
		d, err := time.ParseDuration(sinceStr)
		if err != nil {
			return helperError("parse since: " + err.Error()), nil
		}
		cutoff = time.Now().Add(-d)
	}

	type row struct {
		Provider, Instance         string
		Runs, Failures             int
		UpMs, RunMs, DownMs, Total int64
	}
	agg := map[string]*row{}
	for _, r := range runs {
		if r.Lifecycle == nil {
			continue
		}
		if !cutoff.IsZero() && r.StartedAt.Before(cutoff) {
			continue
		}
		key := r.Lifecycle.Provider + "\x00" + r.Lifecycle.Instance
		inst := r.Lifecycle.Instance
		if groupBy == "provider" {
			key = r.Lifecycle.Provider
			inst = ""
		}
		x := agg[key]
		if x == nil {
			x = &row{Provider: r.Lifecycle.Provider, Instance: inst}
			agg[key] = x
		}
		x.Runs++
		if r.ExitCode != 0 || r.Lifecycle.Error != "" {
			x.Failures++
		}
		x.UpMs += r.Lifecycle.UpMs
		x.RunMs += r.Lifecycle.RunMs
		x.DownMs += r.Lifecycle.DownMs
		x.Total += r.Lifecycle.UpMs + r.Lifecycle.RunMs + r.Lifecycle.DownMs
	}
	out := make([]row, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Total > out[j].Total })
	return helperResult(mustJSON(out)), nil
}

// ---- orphans -------------------------------------------------------------

func handleOrphansList(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	wanted := stringArrayArg(req, "providers")
	want := map[string]bool{}
	for _, p := range wanted {
		want[p] = true
	}
	orphans := []provider.Orphan{}
	for _, p := range provider.Default().All() {
		if len(want) > 0 && !want[p.Name()] {
			continue
		}
		sw, ok := p.(provider.OrphanSweeper)
		if !ok {
			continue
		}
		list, err := sw.ListOrphans(ctx)
		if err != nil {
			continue
		}
		for _, o := range list {
			o.Provider = p.Name()
			orphans = append(orphans, o)
		}
	}
	return helperResult(mustJSON(orphans)), nil
}

func handleOrphansDestroy(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	wanted := stringArrayArg(req, "providers")
	want := map[string]bool{}
	for _, p := range wanted {
		want[p] = true
	}
	type result struct {
		Provider, Name string
		Destroyed      bool
		Err            string `json:",omitempty"`
	}
	var results []result
	for _, p := range provider.Default().All() {
		if len(want) > 0 && !want[p.Name()] {
			continue
		}
		sw, ok := p.(provider.OrphanSweeper)
		if !ok {
			continue
		}
		list, err := sw.ListOrphans(ctx)
		if err != nil {
			continue
		}
		for _, o := range list {
			r := result{Provider: p.Name(), Name: o.Name}
			if err := sw.DeleteOrphan(ctx, o.Name); err != nil {
				r.Err = err.Error()
			} else {
				r.Destroyed = true
			}
			results = append(results, r)
		}
	}
	return helperResult(mustJSON(results)), nil
}

// ---- cancel --------------------------------------------------------------

func handleCancel(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	id := stringArg(req, "runId", "")
	sigName := stringArg(req, "signal", "INT")
	if id == "" {
		return helperError("runId is required"), nil
	}
	cfg, _, err := config.Load()
	if err != nil {
		return helperError(err.Error()), nil
	}
	runDir := filepath.Join(cfg.RunsDir, id)
	st, err := evidence.ReadRunningState(runDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return helperError("not running (no running.lock)"), nil
		}
		return helperError(err.Error()), nil
	}
	sig := syscall.SIGINT
	switch strings.ToUpper(sigName) {
	case "", "INT", "SIGINT":
		sig = syscall.SIGINT
	case "TERM", "SIGTERM":
		sig = syscall.SIGTERM
	case "KILL", "SIGKILL":
		sig = syscall.SIGKILL
	default:
		return helperError(fmt.Sprintf("unknown signal %q", sigName)), nil
	}
	if err := syscall.Kill(st.PID, sig); err != nil {
		return helperError(fmt.Sprintf("kill pid=%d: %v", st.PID, err)), nil
	}
	return helperResult(mustJSON(map[string]any{
		"pid":    st.PID,
		"signal": sigName,
		"runId":  id,
	})), nil
}

// ---- fleet run / image build / notify test (subprocess) ------------------

// vmlabExecutable returns the path to the running vmlab binary so subprocess
// dispatch keeps the agent and the wrapper in sync.
func vmlabExecutable() (string, error) {
	if v := os.Getenv("VMLAB_BIN"); v != "" {
		return v, nil
	}
	return os.Executable()
}

func handleFleetRun(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	tag := stringArg(req, "tag", "")
	if tag == "" {
		return helperError("tag is required"), nil
	}
	flowPath := stringArg(req, "flowPath", "")
	cmdLine := stringArg(req, "command", "")
	if (flowPath == "") == (cmdLine == "") {
		return helperError("provide exactly one of flowPath or command"), nil
	}
	maxParallel := intArg(req, "maxParallel", 0)
	failFast := boolArg(req, "failFast", false)
	retries := intArg(req, "retries", 0)

	bin, err := vmlabExecutable()
	if err != nil {
		return helperError(err.Error()), nil
	}
	args := []string{"run", "@@" + tag, "--json"}
	if flowPath != "" {
		args = append(args, flowPath)
	} else {
		args = append(args, "--", "sh", "-lc", cmdLine)
	}
	if maxParallel > 0 {
		args = append(args, "--max-parallel", strconv.Itoa(maxParallel))
	}
	if failFast {
		args = append(args, "--fail-fast")
	}
	if retries > 0 {
		args = append(args, "--retries", strconv.Itoa(retries))
	}
	return runSubprocess(ctx, bin, args)
}

func handleImageBuild(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	inst := stringArg(req, "instance", "")
	flowPath := stringArg(req, "flowPath", "")
	name := stringArg(req, "name", "")
	desc := stringArg(req, "description", "")
	keep := boolArg(req, "keep", false)
	if inst == "" || flowPath == "" || name == "" {
		return helperError("instance, flowPath, and name are required"), nil
	}
	bin, err := vmlabExecutable()
	if err != nil {
		return helperError(err.Error()), nil
	}
	args := []string{"image", "build", "@" + inst, flowPath, "--name", name}
	if desc != "" {
		args = append(args, "--description", desc)
	}
	if keep {
		args = append(args, "--keep")
	}
	return runSubprocess(ctx, bin, args)
}

func handleNotifyTest(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	phase := stringArg(req, "phase", "all")
	bin, err := vmlabExecutable()
	if err != nil {
		return helperError(err.Error()), nil
	}
	args := []string{"notify", "test", "--phase", phase}
	return runSubprocess(ctx, bin, args)
}

// handleMatrixRun is the agent-friendly entry-point: same lifecycle as
// fleet_run / instance_run but with --format=matrix so the agent gets
// ND-JSON rows with capped stderr tails instead of full logs. Works for
// any vmlab selector (single target, @<inst>, @@<tag>, plain target name,
// or "all").
func handleMatrixRun(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	sel := stringArg(req, "selector", "")
	if sel == "" {
		return helperError("selector is required"), nil
	}
	flowPath := stringArg(req, "flowPath", "")
	cmdLine := stringArg(req, "command", "")
	if (flowPath == "") == (cmdLine == "") {
		return helperError("provide exactly one of flowPath or command"), nil
	}
	fromSnap := stringArg(req, "fromSnapshot", "")
	retries := intArg(req, "retries", 0)

	bin, err := vmlabExecutable()
	if err != nil {
		return helperError(err.Error()), nil
	}
	args := []string{"run", sel, "--format=matrix"}
	if flowPath != "" {
		args = append(args, flowPath)
	} else {
		args = append(args, "--", "sh", "-lc", cmdLine)
	}
	if fromSnap != "" {
		args = append(args, "--from-snapshot", fromSnap)
	}
	if retries > 0 {
		args = append(args, "--retries", strconv.Itoa(retries))
	}
	return runSubprocess(ctx, bin, args)
}

// handleGrant runs `vmlab grant` as a subprocess and packages the result
// with a `needsHumanTouchID` hint the agent can use to render a one-line
// "Touch ID once and I'll continue" UI affordance. The subprocess is the
// same code path the CLI uses, so behaviour is identical (auto-navigation
// via guiport, polling until the binary's doctor reports the scope, etc).
func handleGrant(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	binary := stringArg(req, "binary", "")
	if binary == "" {
		return helperError("binary is required"), nil
	}
	scope := stringArg(req, "scope", "screen-recording")
	auto := boolArg(req, "auto", false)
	noWait := boolArg(req, "noWait", false)
	timeoutStr := stringArg(req, "timeout", "")

	bin, err := vmlabExecutable()
	if err != nil {
		return helperError(err.Error()), nil
	}
	args := []string{"grant", binary, scope}
	if auto {
		args = append(args, "--auto")
	}
	if noWait {
		args = append(args, "--no-wait")
	}
	if timeoutStr != "" {
		args = append(args, "--timeout", timeoutStr)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	exit := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exit = ee.ExitCode()
		} else {
			return helperError(runErr.Error()), nil
		}
	}
	out := map[string]any{
		"binary":            binary,
		"scope":             scope,
		"exit":              exit,
		"stdout":            stdout.String(),
		"stderr":            stderr.String(),
		"needsHumanTouchID": exit != 0 && !noWait,
	}
	if exit == 0 {
		out["granted"] = true
	}
	return helperResult(mustJSON(out)), nil
}

// runSubprocess runs the vmlab binary, captures stdout+stderr, and returns
// them as JSON. The agent gets a structured result regardless of whether the
// underlying command emits JSON itself.
func runSubprocess(ctx context.Context, bin string, args []string) (*mcpgo.CallToolResult, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			return helperError(err.Error()), nil
		}
	}
	return helperResult(mustJSON(map[string]any{
		"exit":   exit,
		"argv":   append([]string{filepath.Base(bin)}, args...),
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	})), nil
}
