package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// undermouseTransport drives the `um` CLI (the headless face of UnderMouse).
// It's a sibling to the guiport transport: both are macOS-local and both
// implement GUI actions, but `um` adds the LLM-side surface — `act` with a
// confirmed-action plan, `context` capture, `ask` / `summarize` / `rewrite`.
//
// Click/type/hotkey kinds round-trip through `um act` which routes them to
// guiport internally via UnderMouse's safe action runtime — so a guiport
// grant gets you all of these. Pure shell `run:` steps execute locally just
// like the guiport transport, so flows can mix `run:` and `gui:` cleanly.
type undermouseTransport struct{ bin string }

// NewUnderMouse returns the undermouse transport.
func NewUnderMouse() Transport { return &undermouseTransport{bin: "um"} }

func (u *undermouseTransport) Name() string { return "undermouse" }

func (u *undermouseTransport) Capabilities() Caps {
	return Caps{Shell: true, GUI: true, Screenshot: true}
}

func (u *undermouseTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(u.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH (install via ~/Projects/undermouse: `make install`)", u.bin)}
	}
	out, _ := captureOutput(ctx, u.bin, []string{"doctor"})
	// `um doctor` emits JSON; we don't need to parse it for liveness — exit
	// code is enough. Surface a short summary for the operator.
	msg := "um doctor ok"
	if strings.Contains(out, `"accessibility":false`) {
		msg = "um doctor ok but accessibility=false (run: vmlab grant um accessibility)"
	} else if strings.Contains(out, `"screenRecording":false`) {
		msg = "um doctor ok but screenRecording=false (run: vmlab grant um screen-recording)"
	}
	return Health{OK: true, Message: msg}
}

// Run executes argv on the local host. UnderMouse targets are macOS-local
// just like guiport — shell steps don't go through `um`.
func (u *undermouseTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{ExitCode: 0}, nil
	}
	return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
}

func (u *undermouseTransport) Sync(ctx context.Context, t target.Target, src string) error {
	return nil
}

func (u *undermouseTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("undermouse: shell not supported (drive a local terminal directly)")
}

func (u *undermouseTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	res, err := runExternal(ctx, u.bin, []string{"screenshot", path}, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("um screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (u *undermouseTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	switch a.Kind {
	case "screenshot":
		return u.Screenshot(ctx, t, a.Path)
	case "context", "capture":
		args := []string{"context"}
		if a.Extra["screenshot"] == true {
			args = append(args, "--screenshot")
		}
		// Write the JSON context to a path when one is supplied; otherwise
		// drop it into the evidence stdout for the run.
		var stdout io.Writer = io.Discard
		if a.Path != "" {
			f, err := os.Create(a.Path)
			if err != nil {
				return fmt.Errorf("um context: create %s: %w", a.Path, err)
			}
			defer f.Close()
			stdout = f
		}
		res, err := runExternal(ctx, u.bin, args, stdout, io.Discard)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("um context exited %d", res.ExitCode)
		}
		return nil
	case "ask", "summarize", "rewrite":
		if a.Text == "" {
			return fmt.Errorf("um %s requires text", a.Kind)
		}
		args := []string{a.Kind, a.Text}
		if a.Extra["context"] == true {
			args = append(args, "--context")
		}
		if a.Extra["screenshot"] == true {
			args = append(args, "--screenshot")
		}
		var stdout io.Writer = io.Discard
		if a.Path != "" {
			f, err := os.Create(a.Path)
			if err != nil {
				return fmt.Errorf("um %s: create %s: %w", a.Kind, a.Path, err)
			}
			defer f.Close()
			stdout = f
		}
		res, err := runExternal(ctx, u.bin, args, stdout, io.Discard)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("um %s exited %d", a.Kind, res.ExitCode)
		}
		return nil
	case "wait":
		ms := extraInt(a.Extra, "milliseconds")
		if ms == 0 {
			ms = extraInt(a.Extra, "ms")
		}
		if ms < 0 {
			ms = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(ms) * time.Millisecond):
		}
		return nil
	default:
		// All other kinds map onto UnderMouse's safe action runtime via
		// `um act --plan -`. The plan envelope mirrors what the LLM emits
		// in act mode, so flows can encode `click_text`, `type_text`,
		// `press_hotkey`, `click_element`, etc. without learning a second
		// vocabulary.
		tool, args, err := umActPlan(a)
		if err != nil {
			return err
		}
		plan := map[string]any{
			"actions": []map[string]any{
				mergeMap(map[string]any{"tool": tool}, args),
			},
		}
		body, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		return execStdin(ctx, u.bin, []string{"act", "--plan", "-"}, string(body))
	}
}

// umActPlan maps a vmlab GUIAction to the (tool, args) shape UnderMouse's
// ActionProposalParser expects. Returns an error for kinds um doesn't
// model — keeps the contract honest instead of silently dropping fields.
func umActPlan(a GUIAction) (string, map[string]any, error) {
	switch a.Kind {
	case "click":
		return "click_element", map[string]any{"selector": a.Selector}, nil
	case "click-text":
		return "click_text", map[string]any{"text": a.Text}, nil
	case "click-at":
		return "click_point", map[string]any{
			"x": extraInt(a.Extra, "x"),
			"y": extraInt(a.Extra, "y"),
		}, nil
	case "type":
		return "type_text", map[string]any{"text": a.Text}, nil
	case "hotkey":
		chord := a.Text
		if chord == "" {
			chord = a.Selector
		}
		return "press_hotkey", map[string]any{"shortcut": chord}, nil
	case "open-url":
		return "open_url", map[string]any{"url": a.Path}, nil
	}
	return "", nil, fmt.Errorf("undermouse: unsupported gui kind %q", a.Kind)
}

// mergeMap returns a new map with all of src + override. Keys in override win.
func mergeMap(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// exec is a small wrapper that captures stdout+stderr into one string —
// used by Doctor to peek at JSON. Distinct from runExternal because we
// don't care about exit code splitting here.
func captureOutput(ctx context.Context, bin string, args []string) (string, error) {
	var buf strings.Builder
	_, err := runExternal(ctx, bin, args, &buf, &buf)
	return buf.String(), err
}

// execStdin runs <bin args...> with stdin piped from `input`. Used for
// `um act --plan -` so the plan JSON doesn't need to land on disk.
func execStdin(ctx context.Context, bin string, args []string, input string) error {
	res, err := runExternalStdin(ctx, bin, args, strings.NewReader(input), io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s %s exited %d", bin, strings.Join(args, " "), res.ExitCode)
	}
	return nil
}
