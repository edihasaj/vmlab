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

// simctlVerbs is the set of cmd[0] values that vmlab routes through
// `xcrun simctl`. Anything else is treated as a host-side shell command
// (the simulator lives on the dev machine, so flows often want a mix
// of simctl verbs and plain host shell — same model as guiport/abx).
var simctlVerbs = map[string]bool{
	"install": true, "uninstall": true, "launch": true, "terminate": true,
	"boot": true, "shutdown": true, "openurl": true,
	"list": true, "io": true, "ui": true, "status_bar": true, "push": true,
	"spawn": true, "privacy": true, "pbcopy": true, "pbpaste": true,
	"create": true, "delete": true, "erase": true, "rename": true, "clone": true,
	"keychain": true, "logverbose": true, "diagnose": true, "bootstatus": true,
	"listapps": true, "get_app_container": true,
}

func (s *simctlTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("simctl: empty command")
	}
	udid := simUDID(t)
	// run:/assert: arrive wrapped as `sh -lc <cmd>` — those are host shell
	// commands, not simctl verbs. Execute them on the host so flows can
	// freely mix `run:` (shell) and `exec:` (simctl verbs) on the same
	// simctl target. Same model as guiport/abx — the simulator lives on
	// the dev machine.
	if IsHostShellArgv(cmd) {
		return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
	}
	// Non-simctl verbs in exec: form also run on the host (so an exec:
	// step can shell out to `osascript`, `screencapture`, etc., against
	// a simctl-tagged target).
	if !simctlVerbs[cmd[0]] {
		return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
	}
	args := []string{"simctl"}
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

func (s *simctlTransport) GUI(ctx context.Context, t target.Target, action GUIAction, stdout, stderr io.Writer) error {
	return fmt.Errorf("simctl: gui actions go through Maestro or idb")
}

func simUDID(t target.Target) string { return t.SettingString("simctl", "udid") }
