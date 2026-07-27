package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
)

// localTransport runs commands on the local host. Useful for tests and for
// targeting the dev machine itself (e.g. `transport: local`).
type localTransport struct{}

// NewLocal returns the local transport.
func NewLocal() Transport { return &localTransport{} }

func (l *localTransport) Name() string { return "local" }

func (l *localTransport) Capabilities() Caps {
	return localCapabilities(runtime.GOOS)
}

func localCapabilities(goos string) Caps {
	caps := Caps{Shell: true, Sync: false, Install: true}
	if goos == "windows" {
		caps.Screenshot = true
		caps.GUI = true
	}
	return caps
}

func (l *localTransport) Doctor(ctx context.Context, t target.Target) Health {
	return Health{OK: true, Message: "local"}
}

func (l *localTransport) Sync(ctx context.Context, t target.Target, src string) error {
	return nil
}

func (l *localTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("local: empty command")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	start := time.Now()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Stdout = stdout
	c.Stderr = stderr
	if dir := t.SettingString("local", "cwd"); dir != "" {
		c.Dir = dir
	}
	err := c.Run()
	res := Result{Duration: time.Since(start).Milliseconds()}
	if err == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}

func (l *localTransport) Shell(ctx context.Context, t target.Target) error {
	sh := os.Getenv("SHELL")
	if sh == "" {
		if runtime.GOOS == "windows" {
			// $SHELL is normally unset on Windows; prefer %ComSpec% (cmd.exe).
			if sh = os.Getenv("ComSpec"); sh == "" {
				sh = "cmd.exe"
			}
		} else {
			sh = "/bin/sh"
		}
	}
	return shellInteractive(ctx, sh, nil)
}

func (l *localTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("local: screenshot not supported")
	}
	if path == "" {
		return fmt.Errorf("local: screenshot needs a destination path")
	}
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms;",
		"Add-Type -AssemblyName System.Drawing;",
		"$b = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds;",
		"$bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height;",
		"$g = [System.Drawing.Graphics]::FromImage($bmp);",
		"$g.CopyFromScreen($b.Location, [System.Drawing.Point]::Empty, $b.Size);",
		"$bmp.Save(" + posixSingleQuote(path) + ", [System.Drawing.Imaging.ImageFormat]::Png);",
		"$g.Dispose(); $bmp.Dispose();",
	}, " ")
	res, err := runLocalPowerShell(ctx, script, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("local: screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (l *localTransport) GUI(ctx context.Context, t target.Target, a GUIAction, stdout, stderr io.Writer) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("local: gui not supported")
	}
	if a.Kind == "wait" {
		ms := extraInt(a.Extra, "milliseconds")
		if ms == 0 {
			ms = extraInt(a.Extra, "ms")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(max(ms, 0)) * time.Millisecond):
			return nil
		}
	}
	if a.Kind == "screenshot" {
		return l.Screenshot(ctx, t, a.Path)
	}
	script, err := winuiScript(a)
	if err != nil {
		return err
	}
	res, err := runLocalPowerShell(ctx, script, stdout, stderr)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("local: gui %s exited %d", a.Kind, res.ExitCode)
	}
	return nil
}

func runLocalPowerShell(ctx context.Context, script string, stdout, stderr io.Writer) (Result, error) {
	return runExternal(
		ctx,
		"powershell.exe",
		[]string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell("& { " + script + " }")},
		stdout,
		stderr,
	)
}
