package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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
	return Caps{Shell: true, Sync: false, Install: true}
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
	return fmt.Errorf("local: screenshot not supported")
}

func (l *localTransport) GUI(ctx context.Context, t target.Target, a GUIAction, stdout, stderr io.Writer) error {
	return fmt.Errorf("local: gui not supported")
}
