package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

// newCpCmd copies a local file into a guest without a shared folder. It exists
// because `sync` only wires up host-side Parallels shared folders (which live on
// the Mac that owns the VM, not necessarily the machine driving vmlab), so
// pushing a one-off script/config to the guest previously meant hand-rolling a
// base64 → WriteAllText incantation through `run`. This makes that a first-class
// command.
func newCpCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "cp <target> <local-path> <remote-path>",
		Short: "Copy a local file into a guest (host→guest), no shared folder needed",
		Long: `Copy a host file into the guest filesystem.

The file is base64-encoded and reconstructed on the guest in chunks, so it
survives the transport's quoting layers and needs no shared folder. Works on
windows guests (rebuilt via PowerShell) and posix guests (via base64).

Use forward slashes for windows remote paths (e.g. C:/Users/me/script.ps1) —
they are valid in .NET/PowerShell and avoid host-shell backslash mangling.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, tr, err := lookupTargetAny(args[0])
			if err != nil {
				return err
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				return fmt.Errorf("cp: read %s: %w", args[1], err)
			}
			if err := pushFileToGuest(cmd.Context(), tr, t, data, args[2]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "copied %s -> %s:%s (%d bytes)\n", args[1], t.Name, args[2], len(data))
			return nil
		},
	}
	return c
}

// pushFileToGuest streams data to remotePath on the guest by base64-encoding it
// and reconstructing it there in chunks small enough to stay well under the
// transport command-line limits. The base64 is appended to a temp file, then
// decoded into the destination and the temp removed.
func pushFileToGuest(ctx context.Context, tr transport.Transport, t target.Target, data []byte, remotePath string) error {
	b64 := base64.StdEncoding.EncodeToString(data)
	// Each chunk becomes one guest command; after UTF-16LE + base64 re-encoding
	// for the Windows -EncodedCommand hop a chunk this size stays well under the
	// ~32k command-line limit, while keeping round-trips (each a cold guest
	// shell, the dominant cost) low. Large files are still slower the more
	// chunks they need.
	const chunkSize = 8000
	windows := t.OSKind() == "windows"
	tmp := remotePath + ".vmlabcp"

	first := true
	for i := 0; i < len(b64); i += chunkSize {
		end := i + chunkSize
		if end > len(b64) {
			end = len(b64)
		}
		part := b64[i:end]
		var argv []string
		if windows {
			cmdlet := "Add-Content"
			if first {
				cmdlet = "Set-Content" // truncate any stale temp on the first chunk
			}
			argv = []string{"powershell", "-NoProfile", "-Command",
				cmdlet + " -LiteralPath '" + tmp + "' -Value '" + part + "' -NoNewline"}
		} else {
			redir := ">>"
			if first {
				redir = ">"
			}
			argv = []string{"sh", "-c", "printf %s '" + part + "' " + redir + " '" + tmp + "'"}
		}
		if err := runGuestChecked(ctx, tr, t, argv); err != nil {
			return fmt.Errorf("cp: streaming chunk: %w", err)
		}
		first = false
	}
	if first {
		// Empty file: nothing was streamed; create an empty destination.
		if windows {
			return runGuestChecked(ctx, tr, t, []string{"powershell", "-NoProfile", "-Command",
				"Set-Content -LiteralPath '" + remotePath + "' -Value '' -NoNewline"})
		}
		return runGuestChecked(ctx, tr, t, []string{"sh", "-c", ": > '" + remotePath + "'"})
	}

	var decode []string
	if windows {
		decode = []string{"powershell", "-NoProfile", "-Command",
			"[IO.File]::WriteAllBytes('" + remotePath + "',[Convert]::FromBase64String((Get-Content -Raw -LiteralPath '" + tmp + "'))); Remove-Item -LiteralPath '" + tmp + "'"}
	} else {
		decode = []string{"sh", "-c", "base64 -d '" + tmp + "' > '" + remotePath + "' && rm -f '" + tmp + "'"}
	}
	if err := runGuestChecked(ctx, tr, t, decode); err != nil {
		return fmt.Errorf("cp: decoding on guest: %w", err)
	}
	return nil
}

func runGuestChecked(ctx context.Context, tr transport.Transport, t target.Target, argv []string) error {
	var errb strings.Builder
	res, err := tr.Run(ctx, t, argv, io.Discard, &errb)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("exit=%d: %s", res.ExitCode, strings.TrimSpace(errb.String()))
	}
	return nil
}
