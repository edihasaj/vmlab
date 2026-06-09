package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/target"
)

type maestroTransport struct{ bin string }

// NewMaestro returns the Maestro mobile flow transport.
func NewMaestro() Transport { return &maestroTransport{bin: "maestro"} }

func (m *maestroTransport) Name() string { return "maestro" }

func (m *maestroTransport) Capabilities() Caps {
	return Caps{Mobile: true, Screenshot: true}
}

func (m *maestroTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(m.bin) {
		return Health{OK: false, Message: "maestro not on PATH"}
	}
	res, err := runExternal(ctx, m.bin, []string{"--version"}, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: fmt.Sprintf("maestro --version exit=%d", res.ExitCode)}
}

// Run delegates to maestro. Convention:
//   - cmd[0] is a maestro subcommand (test, record, hierarchy, ...)
//   - cmd[0] ending in `.yaml` or `.yml` is treated as a flow file -> `maestro test`
func (m *maestroTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("maestro: empty command")
	}
	// run:/assert: arrive wrapped as `sh -lc <cmd>` — those are host
	// shell commands, not maestro verbs. Execute them on the host so a
	// flow can mix shell steps (mkdir, assert) with `exec:` maestro
	// verbs against the same target. Same pattern as adb/simctl.
	if IsHostShellArgv(cmd) {
		return runExternal(ctx, cmd[0], cmd[1:], stdout, stderr)
	}
	args := maestroDeviceArgs(t)
	if hasYAMLExt(cmd[0]) {
		args = append(args, "test")
		args = append(args, cmd...)
	} else {
		args = append(args, cmd...)
	}
	return runExternal(ctx, m.bin, args, stdout, stderr)
}

func (m *maestroTransport) Sync(ctx context.Context, t target.Target, src string) error { return nil }

func (m *maestroTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("maestro: shell not supported")
}

func (m *maestroTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	args := append(maestroDeviceArgs(t), "screenshot", path)
	res, err := runExternal(ctx, m.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("maestro screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (m *maestroTransport) GUI(ctx context.Context, t target.Target, action GUIAction, stdout, stderr io.Writer) error {
	if action.Kind == "run" || action.Kind == "run-flow" {
		_, err := m.Run(ctx, t, []string{"test", action.Path}, stdout, stderr)
		return err
	}
	return fmt.Errorf("maestro: gui kind %q not implemented", action.Kind)
}

func maestroDeviceArgs(t target.Target) []string {
	var args []string
	if d := t.SettingString("maestro", "device"); d != "" {
		args = append(args, "--device", d)
	}
	if h := t.SettingString("maestro", "host"); h != "" {
		args = append(args, "--host", h)
	}
	if p := t.SettingString("maestro", "port"); p != "" {
		args = append(args, "--port", p)
	}
	return args
}

func hasYAMLExt(s string) bool {
	if len(s) < 5 {
		return false
	}
	return s[len(s)-5:] == ".yaml" || s[len(s)-4:] == ".yml"
}
