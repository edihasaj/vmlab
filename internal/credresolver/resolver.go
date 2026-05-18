// Package credresolver turns credential references into concrete secrets.
//
// Supported reference forms:
//
//	op://Vault/Item/field        → shells out to `op read`
//	env:VAR_NAME                  → reads from os.Getenv
//	<anything else>               → returned as-is (plain literal)
//
// Plain-literal passthrough makes the resolver safe to call on every config
// value: callers don't need to know whether a field is templated.
package credresolver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Resolve returns the concrete secret for s. ctx is honoured for the `op`
// invocation so callers can cap how long a 1Password read can stall.
func Resolve(ctx context.Context, s string) (string, error) {
	switch {
	case strings.HasPrefix(s, "op://"):
		return readOp(ctx, s)
	case strings.HasPrefix(s, "env:"):
		name := strings.TrimPrefix(s, "env:")
		v := os.Getenv(name)
		if v == "" {
			return "", fmt.Errorf("env var %s not set", name)
		}
		return v, nil
	default:
		return s, nil
	}
}

// readOp shells out to `op read <uri>`. Requires the user to be signed in.
func readOp(ctx context.Context, uri string) (string, error) {
	cmd := exec.CommandContext(ctx, "op", "read", uri)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("op read %s: %s", uri, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
