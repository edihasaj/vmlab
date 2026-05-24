package transport

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// abxTransport drives a web target via the abx CLI.
type abxTransport struct{ bin string }

// NewABX returns the abx transport.
func NewABX() Transport { return &abxTransport{bin: "abx"} }

func (a *abxTransport) Name() string { return "abx" }

func (a *abxTransport) Capabilities() Caps {
	return Caps{Web: true, Screenshot: true, GUI: true}
}

func (a *abxTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(a.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH", a.bin)}
	}
	res, err := runExternal(ctx, a.bin, []string{"--help"}, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: "abx ok"}
}

// Run executes the argv on the local host. abx targets are macOS-local
// (Playwright Chromium runs here); shell steps land in the user's local
// shell instead of being misforwarded as abx subcommands. Use `gui:` for
// browser actions, or invoke `abx` directly via run.
func (a *abxTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{ExitCode: 0}, nil
	}
	return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
}

func (a *abxTransport) Sync(ctx context.Context, t target.Target, src string) error { return nil }

func (a *abxTransport) Shell(ctx context.Context, t target.Target) error {
	return shellInteractive(ctx, a.bin, []string{"shell"})
}

func (a *abxTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	args := abxArgs(t, []string{"screenshot", path})
	res, err := runExternal(ctx, a.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("abx screenshot exited %d", res.ExitCode)
	}
	return nil
}

// GUI dispatches a structured gui: step to the matching abx verb. This is
// the TCC-free path for web screenshots and any browser-driven E2E — abx
// captures pixels from its own Playwright-controlled Chromium, so the
// macOS Screen Recording grant is irrelevant. For native macOS apps use
// the guiport (or undermouse) transport instead.
//
// Kinds covered:
//   - screenshot — abx screenshot [path]
//   - click      — abx click <selector>
//   - click-text — abx click "text=<value>" (Playwright's text= engine)
//   - type       — abx type <text> (uses currently focused element)
//   - hotkey     — abx press <key>
//   - wait       — abx wait <selector> when Selector is set, else sleep
//   - observe    — abx accessibility
//   - tree       — abx snapshot
//   - open-url   — abx goto <url>
//   - run        — abx <args from Path> (raw forwarding for advanced cases)
func (a *abxTransport) GUI(ctx context.Context, t target.Target, action GUIAction) error {
	prefix := abxArgs(t, nil) // honours live mode + abx.url defaults
	var verb []string
	switch action.Kind {
	case "screenshot":
		path := action.Path
		if path == "" {
			return fmt.Errorf("abx screenshot requires path")
		}
		verb = []string{"screenshot", path}
	case "click":
		if action.Selector == "" {
			return fmt.Errorf("abx click requires selector")
		}
		verb = []string{"click", action.Selector}
	case "click-text":
		if action.Text == "" {
			return fmt.Errorf("abx click-text requires text")
		}
		// Playwright's `text=` engine accepts a substring match by default.
		verb = []string{"click", "text=" + action.Text}
	case "type":
		if action.Text == "" {
			return fmt.Errorf("abx type requires text")
		}
		verb = []string{"type", action.Text}
	case "hotkey":
		key := action.Text
		if key == "" {
			key = action.Selector
		}
		if key == "" {
			return fmt.Errorf("abx hotkey requires text (key combo)")
		}
		verb = []string{"press", key}
	case "wait":
		if action.Selector != "" {
			verb = []string{"wait", action.Selector}
		} else {
			ms := extraInt(action.Extra, "milliseconds")
			if ms == 0 {
				ms = extraInt(action.Extra, "ms")
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
		}
	case "observe":
		verb = []string{"accessibility"}
	case "tree":
		verb = []string{"snapshot"}
	case "open-url":
		url := action.Path
		if url == "" {
			url = action.Text
		}
		if url == "" {
			return fmt.Errorf("abx open-url requires path or text (the URL)")
		}
		verb = []string{"goto", url}
	default:
		return fmt.Errorf("abx: unsupported gui kind %q", action.Kind)
	}
	args := append(prefix, verb...)
	res, err := runExternal(ctx, a.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("abx %s exited %d", action.Kind, res.ExitCode)
	}
	return nil
}

// abxArgs assembles the per-invocation prefix for an abx call. `abx.mode:
// live` wraps every command as `abx live <verb>` to drive the user's real
// Chrome via CDP (needs the chrome-debug helper). The persistent abx
// server keeps URL state across calls, so we don't pass --url here —
// emit a goto step first or use the per-action URL fields.
func abxArgs(t target.Target, extra []string) []string {
	var args []string
	if mode := t.SettingString("abx", "mode"); mode == "live" {
		args = append(args, "live")
	}
	args = append(args, extra...)
	return args
}
