package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/target"
)

// crabboxTransport shells out to the `crabbox` CLI.
type crabboxTransport struct{ bin string }

// NewCrabbox returns the crabbox transport.
func NewCrabbox() Transport { return &crabboxTransport{bin: "crabbox"} }

func (c *crabboxTransport) Name() string { return "crabbox" }

func (c *crabboxTransport) Capabilities() Caps {
	return Caps{Shell: true, Sync: true, Install: true, Screenshot: false, GUI: false}
}

func (c *crabboxTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(c.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH", c.bin)}
	}
	args := []string{"doctor"}
	if cfg := t.SettingString("crabbox", "configPath"); cfg != "" {
		args = append(args, "--config", cfg)
	} else if name := t.SettingString("crabbox", "name"); name != "" {
		args = append(args, name)
	}
	res, err := runExternal(ctx, c.bin, args, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: fmt.Sprintf("crabbox doctor exit=%d", res.ExitCode)}
}

func (c *crabboxTransport) Sync(ctx context.Context, t target.Target, src string) error {
	args := append(targetArgs(t), "sync", src)
	res, err := runExternal(ctx, c.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("crabbox sync exited %d", res.ExitCode)
	}
	return nil
}

func (c *crabboxTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	args := append(targetArgs(t), "run", "--")
	args = append(args, cmd...)
	return runExternal(ctx, c.bin, args, stdout, stderr)
}

func (c *crabboxTransport) Shell(ctx context.Context, t target.Target) error {
	args := append(targetArgs(t), "ssh")
	return shellInteractive(ctx, c.bin, args)
}

func (c *crabboxTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	return fmt.Errorf("crabbox: screenshot not supported")
}

func (c *crabboxTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	return fmt.Errorf("crabbox: gui not supported")
}

// targetArgs translates target settings into crabbox CLI flags.
//
// Supports either:
//   - crabbox.name     -> crabbox --name <n>
//   - crabbox.configPath -> crabbox --config <path>
//   - crabbox.host/.user/.port (inline static)
func targetArgs(t target.Target) []string {
	if cfg := t.SettingString("crabbox", "configPath"); cfg != "" {
		return []string{"--config", cfg}
	}
	if name := t.SettingString("crabbox", "name"); name != "" {
		return []string{"--name", name}
	}
	var args []string
	if host := t.SettingString("crabbox", "host"); host != "" {
		args = append(args, "--host", host)
	}
	if user := t.SettingString("crabbox", "user"); user != "" {
		args = append(args, "--user", user)
	}
	if port := t.SettingString("crabbox", "port"); port != "" {
		args = append(args, "--port", port)
	}
	return args
}
