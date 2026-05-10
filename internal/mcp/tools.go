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
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

type toolHandler func(ctx context.Context, args json.RawMessage) (string, error)

type tool struct {
	name        string
	description string
	inputSchema map[string]any
	handler     toolHandler
}

func defaultTools(allowWrite bool) []tool {
	out := []tool{
		{
			name:        "vmlab_targets",
			description: "List configured targets with their tags and transports.",
			inputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			handler: func(_ context.Context, _ json.RawMessage) (string, error) {
				_, p, err := config.Load()
				if err != nil {
					return "", err
				}
				r, err := target.Load(p)
				if err != nil {
					return "", err
				}
				return mustJSON(r.All()), nil
			},
		},
		{
			name:        "vmlab_doctor",
			description: "Check transport binaries and target reachability.",
			inputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector": map[string]any{"type": "string", "description": "Optional selector (default: all)"},
				},
			},
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var p struct{ Selector string }
				_ = json.Unmarshal(raw, &p)
				if p.Selector == "" {
					p.Selector = "all"
				}
				_, paths, err := config.Load()
				if err != nil {
					return "", err
				}
				r, err := target.Load(paths)
				if err != nil {
					return "", err
				}
				ts, err := target.NewSelector(p.Selector).Resolve(r)
				if err != nil {
					return "", err
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
				return mustJSON(rows), nil
			},
		},
		{
			name:        "vmlab_evidence",
			description: "List recent run summaries (read-only).",
			inputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "default": 20},
				},
			},
			handler: func(_ context.Context, raw json.RawMessage) (string, error) {
				var p struct{ Limit int }
				_ = json.Unmarshal(raw, &p)
				if p.Limit <= 0 {
					p.Limit = 20
				}
				cfg, _, err := config.Load()
				if err != nil {
					return "", err
				}
				runs, err := evidence.List(cfg.RunsDir)
				if err != nil {
					return "", err
				}
				if len(runs) > p.Limit {
					runs = runs[:p.Limit]
				}
				return mustJSON(runs), nil
			},
		},
	}

	if !allowWrite {
		return out
	}

	out = append(out,
		tool{
			name:        "vmlab_run",
			description: "Run a shell command or YAML flow against a target selector.",
			inputSchema: map[string]any{
				"type":     "object",
				"required": []string{"selector"},
				"properties": map[string]any{
					"selector":    map[string]any{"type": "string"},
					"command":     map[string]any{"type": "string", "description": "Shell command (mutually exclusive with flowPath)"},
					"flowPath":    map[string]any{"type": "string", "description": "Path to a flow YAML"},
					"maxParallel": map[string]any{"type": "integer", "default": 0},
					"failFast":    map[string]any{"type": "boolean", "default": false},
				},
			},
			handler: runHandler,
		},
		tool{
			name:        "vmlab_web",
			description: "Run an abx-style web action against a web target.",
			inputSchema: map[string]any{
				"type":     "object",
				"required": []string{"target", "args"},
				"properties": map[string]any{
					"target": map[string]any{"type": "string"},
					"args":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			handler: webHandler,
		},
		tool{
			name:        "vmlab_gui",
			description: "Run a guiport-style desktop action against a gui target.",
			inputSchema: map[string]any{
				"type":     "object",
				"required": []string{"target", "kind"},
				"properties": map[string]any{
					"target":   map[string]any{"type": "string"},
					"kind":     map[string]any{"type": "string", "enum": []string{"click", "type", "screenshot", "run"}},
					"selector": map[string]any{"type": "string"},
					"text":     map[string]any{"type": "string"},
					"path":     map[string]any{"type": "string"},
				},
			},
			handler: guiHandler,
		},
	)
	return out
}

func runHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var p struct {
		Selector    string `json:"selector"`
		Command     string `json:"command"`
		FlowPath    string `json:"flowPath"`
		MaxParallel int    `json:"maxParallel"`
		FailFast    bool   `json:"failFast"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	if p.Selector == "" {
		return "", fmt.Errorf("selector is required")
	}
	if (p.Command == "") == (p.FlowPath == "") {
		return "", fmt.Errorf("provide exactly one of command or flowPath")
	}
	_, paths, err := config.Load()
	if err != nil {
		return "", err
	}
	if err := config.EnsureDirs(paths); err != nil {
		return "", err
	}
	r, err := target.Load(paths)
	if err != nil {
		return "", err
	}
	ts, err := target.NewSelector(p.Selector).Resolve(r)
	if err != nil {
		return "", err
	}
	reg := transport.Default()
	var f *flow.Flow
	if p.FlowPath != "" {
		f, err = flow.Load(p.FlowPath)
		if err != nil {
			return "", err
		}
	}
	run, err := evidence.New(paths.RunsDir)
	if err != nil {
		return "", err
	}
	if f != nil {
		run.SetFlow(f.SourceFile)
	} else {
		run.SetCmd(p.Command)
	}
	run.SetSelector(p.Selector)

	var stdout, stderr bytes.Buffer
	outcomes, runErr := fleet.Run(ctx, ts, reg, fleet.Options{MaxParallel: p.MaxParallel, FailFast: p.FailFast},
		&stdout, &stderr,
		func(ctx context.Context, t target.Target, tr transport.Transport, so, se io.Writer) (int, error) {
			outW, errW, logs, err := run.TargetWriters(t.Name, &stdout, &stderr)
			if err != nil {
				return 0, err
			}
			defer logs.Close()
			if f != nil {
				steps, err := flow.Run(ctx, tr, t, f, outW, errW)
				run.WriteSteps(t.Name, steps)
				if err != nil {
					return lastFlowExit(steps, err), err
				}
				return 0, nil
			}
			res, err := tr.Run(ctx, t, []string{"sh", "-lc", p.Command}, outW, errW)
			return res.ExitCode, err
		})
	exit := fleet.AggregateExit(outcomes)
	for _, o := range outcomes {
		s := evidence.TargetSummary{Name: o.Target.Name, Transport: o.Target.Transport, ExitCode: o.ExitCode, Duration: o.Duration.Milliseconds()}
		if o.Error != nil {
			s.Error = o.Error.Error()
		}
		run.AddTarget(s)
	}
	meta, _ := run.Finish(exit)
	return mustJSON(map[string]any{
		"runId":  meta.ID,
		"exit":   exit,
		"meta":   meta,
		"err":    errOrNil(runErr),
		"stdout": stdout.String(),
		"stderr": stderr.String(),
	}), nil
}

func webHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var p struct {
		Target string   `json:"target"`
		Args   []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	_, paths, err := config.Load()
	if err != nil {
		return "", err
	}
	r, err := target.Load(paths)
	if err != nil {
		return "", err
	}
	t, ok := r.Get(p.Target)
	if !ok {
		return "", fmt.Errorf("unknown target: %s", p.Target)
	}
	tr, err := transport.Default().Get(t.Transport)
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	res, err := tr.Run(ctx, t, p.Args, &stdout, &stderr)
	if err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"exit": res.ExitCode, "stdout": stdout.String(), "stderr": stderr.String()}), nil
}

func guiHandler(ctx context.Context, raw json.RawMessage) (string, error) {
	var p struct {
		Target   string `json:"target"`
		Kind     string `json:"kind"`
		Selector string `json:"selector"`
		Text     string `json:"text"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	_, paths, err := config.Load()
	if err != nil {
		return "", err
	}
	r, err := target.Load(paths)
	if err != nil {
		return "", err
	}
	t, ok := r.Get(p.Target)
	if !ok {
		return "", fmt.Errorf("unknown target: %s", p.Target)
	}
	tr, err := transport.Default().Get(t.Transport)
	if err != nil {
		return "", err
	}
	if err := tr.GUI(ctx, t, transport.GUIAction{Kind: p.Kind, Selector: p.Selector, Text: p.Text, Path: p.Path}); err != nil {
		return "", err
	}
	return mustJSON(map[string]any{"ok": true}), nil
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
