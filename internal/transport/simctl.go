package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/target"
)

// simctlTransport drives an iOS Simulator via `xcrun simctl`.
type simctlTransport struct{ bin string }

// NewSimctl returns the iOS Simulator transport.
func NewSimctl() Transport { return &simctlTransport{bin: "xcrun"} }

func (s *simctlTransport) Name() string { return "simctl" }

func (s *simctlTransport) Capabilities() Caps {
	return Caps{Install: true, Mobile: true, Screenshot: true}
}

func (s *simctlTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(s.bin) {
		return Health{OK: false, Message: "xcrun not on PATH"}
	}
	res, err := runExternal(ctx, s.bin, []string{"simctl", "list", "devices", "available", "-j"}, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: fmt.Sprintf("simctl list exit=%d", res.ExitCode)}
}

func (s *simctlTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	udid := simUDID(t)
	args := []string{"simctl"}
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("simctl: empty command")
	}
	// Common verbs route through simctl with udid as second positional arg.
	switch cmd[0] {
	case "install", "uninstall", "launch", "terminate", "boot", "shutdown", "openurl":
		args = append(args, cmd[0])
		if udid != "" {
			args = append(args, udid)
			args = append(args, cmd[1:]...)
		} else {
			args = append(args, cmd[1:]...)
		}
	default:
		args = append(args, cmd...)
	}
	return runExternal(ctx, s.bin, args, stdout, stderr)
}

func (s *simctlTransport) Sync(ctx context.Context, t target.Target, src string) error { return nil }

func (s *simctlTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("simctl: shell not supported")
}

func (s *simctlTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	udid := simUDID(t)
	if udid == "" {
		udid = "booted"
	}
	args := []string{"simctl", "io", udid, "screenshot", path}
	res, err := runExternal(ctx, s.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("simctl screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (s *simctlTransport) GUI(ctx context.Context, t target.Target, action GUIAction) error {
	return fmt.Errorf("simctl: gui actions go through Maestro or idb")
}

func simUDID(t target.Target) string { return t.SettingString("simctl", "udid") }
