package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// captureOutput runs <bin args...> and returns combined stdout+stderr as a
// string. Callers that only need to peek at a command's output (e.g. Doctor
// reading a JSON liveness blob) use this instead of splitting streams.
func captureOutput(ctx context.Context, bin string, args []string) (string, error) {
	var buf strings.Builder
	_, err := runExternal(ctx, bin, args, &buf, &buf)
	return buf.String(), err
}

// runExternal runs an external command and writes its output to stdout/stderr.
// It returns a Result with exit code & duration. If the command fails to start
// (e.g. binary missing), it returns an error.
func runExternal(ctx context.Context, name string, args []string, stdout, stderr io.Writer) (Result, error) {
	return runExternalEnv(ctx, name, args, nil, stdout, stderr)
}

func runExternalEnv(ctx context.Context, name string, args []string, env []string, stdout, stderr io.Writer) (Result, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	start := time.Now()
	c := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
	c.Stdout = stdout
	c.Stderr = stderr
	err := c.Run()
	res := Result{Duration: time.Since(start).Milliseconds()}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("%s: %w after %dms", name, ctxErr, res.Duration)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return res, fmt.Errorf("%s: not found on PATH (install it or expose via PATH)", name)
	}
	return res, err
}

// runExternalStdin is the runExternal variant that wires a Reader into the
// child's stdin. Used when a transport wants to push structured input (a
// JSON plan, a flow body) without staging it on disk.
func runExternalStdin(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (Result, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	start := time.Now()
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin = stdin
	c.Stdout = stdout
	c.Stderr = stderr
	err := c.Run()
	res := Result{Duration: time.Since(start).Milliseconds()}
	if err == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("%s: %w after %dms", name, ctxErr, res.Duration)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return res, fmt.Errorf("%s: not found on PATH (install it or expose via PATH)", name)
	}
	return res, err
}

// shellInteractive replaces stdin/stdout/stderr with the parent process so the
// invoked binary takes over the terminal (used for `vmlab shell`).
func shellInteractive(ctx context.Context, name string, args []string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s exited %d", name, ee.ExitCode())
		}
		return err
	}
	return nil
}

// haveBinary returns true if the named binary is on PATH.
func haveBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
