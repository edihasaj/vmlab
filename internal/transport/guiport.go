package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/target"
)

// guiportTransport drives a desktop UI via the guiport CLI.
type guiportTransport struct{ bin string }

// NewGuiport returns the guiport transport.
func NewGuiport() Transport { return &guiportTransport{bin: "guiport"} }

func (g *guiportTransport) Name() string { return "guiport" }

func (g *guiportTransport) Capabilities() Caps {
	return Caps{GUI: true, Screenshot: true}
}

func (g *guiportTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(g.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH", g.bin)}
	}
	res, err := runExternal(ctx, g.bin, []string{"doctor"}, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: fmt.Sprintf("guiport doctor exit=%d", res.ExitCode)}
}

func (g *guiportTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	args := guiAppArgs(t)
	args = append(args, cmd...)
	return runExternal(ctx, g.bin, args, stdout, stderr)
}

func (g *guiportTransport) Sync(ctx context.Context, t target.Target, src string) error { return nil }

func (g *guiportTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("guiport: shell not supported")
}

func (g *guiportTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	args := append(guiAppArgs(t), "screenshot", "--out", path)
	res, err := runExternal(ctx, g.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("guiport screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (g *guiportTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	args := guiAppArgs(t)
	switch a.Kind {
	case "click":
		args = append(args, "click", a.Selector)
	case "type":
		args = append(args, "type", "--text", a.Text)
		if a.Selector != "" {
			args = append(args, "--into", a.Selector)
		}
	case "screenshot":
		args = append(args, "screenshot", "--out", a.Path)
	case "run", "run-flow":
		args = append(args, "run", a.Path)
	default:
		return fmt.Errorf("guiport: unknown action kind %q", a.Kind)
	}
	for k, v := range a.Extra {
		args = append(args, "--"+k, fmt.Sprintf("%v", v))
	}
	res, err := runExternal(ctx, g.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		// surface failure with the action for context
		b, _ := json.Marshal(a)
		return fmt.Errorf("guiport %s exited %d (action=%s)", a.Kind, res.ExitCode, string(b))
	}
	return nil
}

func guiAppArgs(t target.Target) []string {
	var args []string
	if app := t.SettingString("guiport", "app"); app != "" {
		args = append(args, "--app", app)
	}
	if t.Setting("guiport", "strict") == true {
		args = append(args, "--strict")
	}
	return args
}
