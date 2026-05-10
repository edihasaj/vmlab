package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/target"
)

// abxTransport drives a web target via the abx CLI.
type abxTransport struct{ bin string }

// NewABX returns the abx transport.
func NewABX() Transport { return &abxTransport{bin: "abx"} }

func (a *abxTransport) Name() string { return "abx" }

func (a *abxTransport) Capabilities() Caps {
	return Caps{Web: true, Screenshot: true}
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

// Run forwards args to abx. Convention:
//   - if first arg starts with "live", we pass through as `abx live <rest>`
//   - otherwise we pass args verbatim
func (a *abxTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	args := abxArgs(t, cmd)
	return runExternal(ctx, a.bin, args, stdout, stderr)
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

func (a *abxTransport) GUI(ctx context.Context, t target.Target, action GUIAction) error {
	return fmt.Errorf("abx: gui actions go through web subcommands")
}

func abxArgs(t target.Target, extra []string) []string {
	var args []string
	if mode := t.SettingString("abx", "mode"); mode == "live" {
		args = append(args, "live")
	}
	if url := t.SettingString("abx", "url"); url != "" {
		args = append(args, "--url", url)
	}
	args = append(args, extra...)
	return args
}
