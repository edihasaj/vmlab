package transport

import (
	"context"
	"fmt"
	"io"

	"github.com/edihasaj/vmlab/internal/target"
)

type idbTransport struct{ bin string }

// NewIDB returns the iOS idb transport.
func NewIDB() Transport { return &idbTransport{bin: "idb"} }

func (i *idbTransport) Name() string { return "idb" }

func (i *idbTransport) Capabilities() Caps {
	return Caps{Shell: false, Install: true, Mobile: true, Screenshot: true}
}

func (i *idbTransport) Doctor(ctx context.Context, t target.Target) Health {
	if !haveBinary(i.bin) {
		return Health{OK: false, Message: "idb not on PATH"}
	}
	args := append(idbUDIDArgs(t), "list-targets")
	res, err := runExternal(ctx, i.bin, args, io.Discard, io.Discard)
	if err != nil {
		return Health{OK: false, Message: err.Error()}
	}
	return Health{OK: res.ExitCode == 0, Message: fmt.Sprintf("idb list-targets exit=%d", res.ExitCode)}
}

func (i *idbTransport) Run(ctx context.Context, t target.Target, cmd []string, stdout, stderr io.Writer) (Result, error) {
	args := idbUDIDArgs(t)
	args = append(args, cmd...)
	return runExternal(ctx, i.bin, args, stdout, stderr)
}

// Sync is unsupported for idb. iOS sandboxing means file deployment is always
// bundle-scoped (`idb file push --bundle-id <id> <src> <remote>`), not the
// generic "copy a working tree into the device" the Sync contract assumes.
// Use the Run path with explicit `file push --bundle-id …` args instead.
func (i *idbTransport) Sync(ctx context.Context, t target.Target, src string) error {
	return fmt.Errorf("idb: sync is bundle-scoped on iOS — use `run -- file push --bundle-id <id> %s <remote>`", src)
}

func (i *idbTransport) Shell(ctx context.Context, t target.Target) error {
	return fmt.Errorf("idb: shell not supported")
}

func (i *idbTransport) Screenshot(ctx context.Context, t target.Target, path string) error {
	args := append(idbUDIDArgs(t), "screenshot", path)
	res, err := runExternal(ctx, i.bin, args, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("idb screenshot exited %d", res.ExitCode)
	}
	return nil
}

func (i *idbTransport) GUI(ctx context.Context, t target.Target, action GUIAction) error {
	return fmt.Errorf("idb: gui actions go through Maestro")
}

func idbUDIDArgs(t target.Target) []string {
	if u := t.SettingString("idb", "udid"); u != "" {
		return []string{"--udid", u}
	}
	return nil
}
