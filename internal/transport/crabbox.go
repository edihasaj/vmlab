package transport

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/edihasaj/vmlab/internal/target"
)

// crabboxTransport shells out to the `crabbox` CLI (>= 0.21).
//
// crabbox is lease-based: a box is addressed by an id/slug (`-id`), optionally
// scoped to a `-provider`, or pinned to a static SSH host via `-static-host`
// /`-static-user`/`-static-port`. It uses Go's flag package, so flags are
// single-dash and live *after* the subcommand. crabbox has no standalone
// `sync` command — `run` rsyncs the working checkout to the box on every call.
type crabboxTransport struct{ bin string }

// NewCrabbox returns the crabbox transport.
func NewCrabbox() Transport { return &crabboxTransport{bin: "crabbox"} }

func (c *crabboxTransport) Name() string { return "crabbox" }

func (c *crabboxTransport) Capabilities() Caps {
	// Screenshot is supported via `crabbox screenshot` on desktop leases.
	// GUI (AX/OCR driving) stays with guiport.
	return Caps{Shell: true, Sync: true, Install: true, Screenshot: true, GUI: false}
}

func (c *crabboxTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(c.bin) {
		return Health{OK: false, Message: fmt.Sprintf("%s not on PATH (install from openclaw/crabbox)", c.bin)}
	}
	// When the target names a specific lease, check that lease's reachability;
	// otherwise fall back to crabbox's global broker/provider readiness check.
	var args []string
	if hasLeaseAddr(t) {
		args = append([]string{"status"}, crabboxAddr(t)...)
		args = append(args, "-json")
	} else {
		args = []string{"doctor"}
	}
	res, err := runExternal(ctx, c.bin, args, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: fmt.Sprintf("crabbox %s exit=%d", args[0], res.ExitCode)}
}

// Sync pushes the local checkout to the box. crabbox has no standalone sync, so
// we run a no-op remote command — `run` rsyncs the diff before executing.
func (c *crabboxTransport) Sync(ctx context.Context, t target.Target, src string) error {
	args := crabboxRunArgs(t, []string{"true"})
	res, err := runExternal(ctx, c.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("crabbox sync (run true) exited %d", res.ExitCode)
	}
	return nil
}

func (c *crabboxTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	return runExternal(ctx, c.bin, crabboxRunArgs(t, cmd), stdout, stderr)
}

// Shell opens an interactive session. `crabbox ssh` only *prints* the ssh
// command for a lease, so we capture it and hand it to the local shell.
func (c *crabboxTransport) Shell(ctx context.Context, t target.Target) error {
	args := append([]string{"ssh"}, crabboxAddr(t)...)
	out, err := captureOutput(ctx, c.bin, args)
	if err != nil {
		return err
	}
	sshCmd := strings.TrimSpace(out)
	if sshCmd == "" {
		return fmt.Errorf("crabbox ssh: no command returned (is the lease running?)")
	}
	return shellInteractive(ctx, "sh", []string{"-c", sshCmd})
}

func (c *crabboxTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	args := append([]string{"screenshot"}, crabboxAddr(t)...)
	args = append(args, "-output", path)
	res, err := runExternal(ctx, c.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("crabbox screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (c *crabboxTransport) GUI(ctx context.Context, t target.Target, a GUIAction) error {
	// crabbox can grab a frame; AX/OCR driving belongs to guiport.
	if a.Kind == "screenshot" {
		return c.Screenshot(ctx, t, a.Path)
	}
	return fmt.Errorf("crabbox: gui %q not supported (use a guiport target)", a.Kind)
}

// crabboxRunArgs builds `run <addr> -- <cmd...>`. Addressing flags must follow
// the subcommand (Go flag package) and precede the `--` command separator.
func crabboxRunArgs(t target.Target, cmd []string) []string {
	args := append([]string{"run"}, crabboxAddr(t)...)
	args = append(args, "--")
	return append(args, cmd...)
}

// hasLeaseAddr reports whether the target names a concrete lease (id/slug) as
// opposed to relying on crabbox's repo-local default.
func hasLeaseAddr(t target.Target) bool {
	return t.SettingString("crabbox", "id") != "" ||
		t.SettingString("crabbox", "slug") != "" ||
		firstNonEmpty(t.SettingString("crabbox", "staticHost"), t.SettingString("crabbox", "host")) != ""
}

// crabboxAddr translates target settings into crabbox addressing flags.
//
// Recognised settings (all under the `crabbox` namespace):
//   - id / slug                        -> -id <v> (lease id or friendly slug)
//   - provider                         -> -provider <v>
//   - staticHost/staticUser/staticPort (aliases: host/user/port)
//     -> -static-host/-static-user/-static-port, defaulting -provider ssh
func crabboxAddr(t target.Target) []string {
	var args []string
	if id := t.SettingString("crabbox", "id"); id != "" {
		args = append(args, "-id", id)
	} else if slug := t.SettingString("crabbox", "slug"); slug != "" {
		args = append(args, "-id", slug)
	}

	provider := t.SettingString("crabbox", "provider")

	host := firstNonEmpty(t.SettingString("crabbox", "staticHost"), t.SettingString("crabbox", "host"))
	user := firstNonEmpty(t.SettingString("crabbox", "staticUser"), t.SettingString("crabbox", "user"))
	port := firstNonEmpty(t.SettingString("crabbox", "staticPort"), t.SettingString("crabbox", "port"))
	if host != "" {
		if provider == "" {
			provider = "ssh"
		}
		args = append(args, "-static-host", host)
		if user != "" {
			args = append(args, "-static-user", user)
		}
		if port != "" {
			args = append(args, "-static-port", port)
		}
	}

	if provider != "" {
		args = append(args, "-provider", provider)
	}
	return args
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
