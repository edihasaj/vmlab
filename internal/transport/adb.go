package transport

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/edihasaj/vmlab/internal/target"
)

type adbTransport struct{ bin string }

// NewADB returns the Android adb transport.
func NewADB() Transport { return &adbTransport{bin: "adb"} }

func (a *adbTransport) Name() string { return "adb" }

func (a *adbTransport) Capabilities() Caps {
	return Caps{Shell: true, Sync: true, Install: true, Mobile: true, Screenshot: true}
}

func (a *adbTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(a.bin) {
		return Health{OK: false, Message: "adb not on PATH"}
	}
	args := append(adbSerialArgs(t), "get-state")
	res, err := runExternal(ctx, a.bin, args, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	if res.ExitCode != 0 {
		return Health{OK: false, Message: "adb get-state failed (device offline?)"}
	}
	return Health{OK: true, Message: "adb device online"}
}

func (a *adbTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	args := adbSerialArgs(t)
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("adb: empty command")
	}
	first := cmd[0]
	switch first {
	case "shell", "install", "uninstall", "push", "pull", "logcat", "reboot", "forward", "reverse":
		args = append(args, cmd...)
	default:
		// Default: run as adb shell <cmd...>
		args = append(args, "shell")
		args = append(args, strings.Join(cmd, " "))
	}
	return runExternal(ctx, a.bin, args, stdout, stderr)
}

// Sync pushes src into the device via `adb push`. The remote destination is
// configurable via adb.dest (defaults to /sdcard/vmlab). The default landing
// pad is /sdcard because that's the only path every non-rooted Android device
// is guaranteed to let `adb push` write to.
func (a *adbTransport) Sync(ctx context.Context, t target.Target, src string) error {
	if src == "" {
		return fmt.Errorf("adb: sync requires a source path")
	}
	dest := t.SettingString("adb", "dest")
	if dest == "" {
		dest = "/sdcard/vmlab"
	}
	args := append(adbSerialArgs(t), "push", src, dest)
	res, err := runExternal(ctx, a.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("adb push exited %d", res.ExitCode)
	}
	return nil
}

func (a *adbTransport) Shell(ctx context.Context, t target.Target) error {
	args := append(adbSerialArgs(t), "shell")
	return shellInteractive(ctx, a.bin, args)
}

func (a *adbTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	args := append(adbSerialArgs(t), "exec-out", "screencap", "-p")
	// Capture to file via stdout redirection.
	// Simpler approach: pull via screencap on device then adb pull.
	// We'll write to local path by writing exec-out output to file.
	f, err := openCreate(path)
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := runExternal(ctx, a.bin, args, f, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("adb screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (a *adbTransport) GUI(ctx context.Context, t target.Target, action GUIAction) error {
	return fmt.Errorf("adb: gui actions go through Maestro or input commands")
}

func adbSerialArgs(t target.Target) []string {
	if s := t.SettingString("adb", "serial"); s != "" {
		return []string{"-s", s}
	}
	return nil
}
