package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newGrantCmd registers `vmlab grant <binary> <scope>` — opens the right
// macOS System Settings pane and polls until the requested TCC scope is
// detectable for the binary. The actual toggle still requires a user
// Touch ID / admin password, but the navigation and verification are
// automated so the loop completes in one step instead of five.
//
// macOS won't let any process write to TCC.db directly (SIP-protected),
// and even with Accessibility it can't bypass the auth prompt at the
// moment of the toggle. This subcommand is the agentic floor — clicks
// reduced to the single hardware-attested gesture macOS still requires.
func newGrantCmd() *cobra.Command {
	var (
		noWait  bool
		auto    bool
		dryRun  bool
		timeout time.Duration
	)
	c := &cobra.Command{
		Use:   "grant <binary> [scope]",
		Short: "Open System Settings for a TCC grant + poll until detectable",
		Long: `Open the macOS Privacy & Security pane for the given scope, instruct
the user to toggle <binary> ON, and poll the binary's doctor until the
grant is observable.

Scopes:
  screen-recording   (default)  Screen Recording capture
  accessibility                 AX reads + UI actions
  input-monitoring              Listening to global keyboard/mouse
  full-disk-access              Full Disk Access
  automation                    Apple events to other apps

Examples:
  vmlab grant guiport screen-recording
  vmlab grant guiport accessibility
  vmlab grant um screen-recording --timeout 2m`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				return fmt.Errorf("vmlab grant is macOS-only (TCC is an Apple concept)")
			}
			binary := args[0]
			scope := "screen-recording"
			if len(args) == 2 {
				scope = args[1]
			}
			url, err := tccScopeURL(scope)
			if err != nil {
				return err
			}

			w := cmd.ErrOrStderr()
			if dryRun {
				fmt.Fprintf(w, "[dry-run] would open: %s\n", url)
				fmt.Fprintf(w, "[dry-run] would prompt user to toggle %q in %s\n", binary, prettyScope(scope))
				if auto {
					fmt.Fprintf(w, "[dry-run] would invoke guiport click-text %q to focus the row\n", binary)
				}
				if !noWait {
					fmt.Fprintf(w, "[dry-run] would poll the binary's doctor up to %s\n", timeout)
				}
				return nil
			}

			fmt.Fprintf(w, "Opening System Settings → Privacy & Security → %s\n", prettyScope(scope))
			fmt.Fprintf(w, "Find '%s' in the list and toggle it ON.\n", binary)
			fmt.Fprintf(w, "macOS will prompt for Touch ID / admin password — that's the one gesture we can't automate.\n\n")

			if err := exec.Command("open", url).Run(); err != nil {
				return fmt.Errorf("open settings: %w", err)
			}

			if auto {
				// Give the pane a moment to render before guiport starts
				// poking at the AX tree. 800ms is generous on a hot SSD,
				// safe on a cold one, and well under typical TID timeouts.
				time.Sleep(800 * time.Millisecond)
				if err := autoNavigateTCC(cmd.Context(), w, binary); err != nil {
					// Auto-navigation is best-effort; the pane is open
					// regardless. Print the warning and continue to the
					// polling loop so the human can complete the grant.
					fmt.Fprintf(w, "auto-navigate: %v (continuing — pane is open)\n", err)
				} else {
					fmt.Fprintf(w, "auto-navigate: found %q row, toggle clicked — complete the Touch ID prompt to finish\n", binary)
				}
			}

			if noWait {
				fmt.Fprintln(w, "settings pane opened; not waiting for grant (--no-wait)")
				return nil
			}

			verifier, vname := scopeVerifier(scope, binary)
			if verifier == nil {
				fmt.Fprintf(w, "no automated verifier for (%s, %s); pane opened — press Ctrl-C when satisfied.\n", binary, scope)
				return nil
			}
			fmt.Fprintf(w, "waiting up to %s — polling via %s\n", timeout, vname)

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				if verifier(ctx) {
					fmt.Fprintf(w, "✓ %s granted for %s\n", prettyScope(scope), binary)
					return nil
				}
				select {
				case <-ctx.Done():
					return fmt.Errorf("timed out after %s waiting for %s grant to %s", timeout, prettyScope(scope), binary)
				case <-ticker.C:
				}
			}
		},
	}
	c.Flags().BoolVar(&noWait, "no-wait", false, "open the pane and exit instead of polling")
	c.Flags().BoolVar(&auto, "auto", false, "use guiport to scroll to and click the binary's toggle (requires Accessibility already granted to guiport); Touch ID is still the human's job")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print what would happen without opening System Settings or polling")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "max time to wait for the grant")
	return c
}

// autoNavigateTCC drives the just-opened Privacy & Security pane via guiport
// so the user only has to authenticate. The pane's exact layout varies across
// macOS versions, so this is best-effort:
//
//  1. click-text "<binary>" — locates and highlights the row.
//  2. click-text "<binary>" again as a focus hint (some panes need a second
//     interaction before the toggle becomes the keyboard target).
//
// The actual toggle flip is left to System Settings' own keyboard behaviour
// (space when the row is focused) plus the Touch ID prompt that follows.
// We don't try to find and click the toggle itself — its AX path moves
// every couple OS revs and a wrong click is worse than no click.
func autoNavigateTCC(ctx context.Context, w interface{ Write(p []byte) (int, error) }, binary string) error {
	if _, err := exec.LookPath("guiport"); err != nil {
		return fmt.Errorf("guiport not on PATH; install it or run without --auto")
	}
	// Tell System Settings to take focus so subsequent AX queries see the
	// just-opened pane, not whatever window happened to be frontmost.
	_ = exec.CommandContext(ctx, "osascript", "-e", `tell application "System Settings" to activate`).Run()
	time.Sleep(400 * time.Millisecond)

	ctxRow, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctxRow, "guiport", "click-text", binary, "--app", "System Settings").CombinedOutput()
	if err != nil {
		// Try without the --app scope; some panes register as the legacy
		// "System Preferences" process even on modern macOS.
		out2, err2 := exec.CommandContext(ctxRow, "guiport", "click-text", binary).CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("click-text %q: %s", binary, strings.TrimSpace(string(out)+string(out2)))
		}
	}
	return nil
}

// tccScopeURL maps a user-friendly scope name to the x-apple.systempreferences
// URL that opens the right pane in System Settings. Aliases are accepted so
// CLI ergonomics match how people actually say these names.
func tccScopeURL(scope string) (string, error) {
	base := "x-apple.systempreferences:com.apple.preference.security?Privacy_"
	switch strings.ToLower(scope) {
	case "screen-recording", "screen_recording", "screencapture", "sr":
		return base + "ScreenCapture", nil
	case "accessibility", "ax", "a11y":
		return base + "Accessibility", nil
	case "input-monitoring", "input_monitoring", "listen-event", "im":
		return base + "ListenEvent", nil
	case "full-disk-access", "full_disk_access", "fda":
		return base + "AllFiles", nil
	case "automation", "appleevents":
		return base + "Automation", nil
	case "camera":
		return base + "Camera", nil
	case "microphone", "mic":
		return base + "Microphone", nil
	}
	return "", fmt.Errorf("unknown TCC scope %q (try: screen-recording, accessibility, input-monitoring, full-disk-access, automation, camera, microphone)", scope)
}

func prettyScope(scope string) string {
	switch strings.ToLower(scope) {
	case "screen-recording", "screen_recording", "screencapture", "sr":
		return "Screen Recording"
	case "accessibility", "ax", "a11y":
		return "Accessibility"
	case "input-monitoring", "input_monitoring", "listen-event", "im":
		return "Input Monitoring"
	case "full-disk-access", "full_disk_access", "fda":
		return "Full Disk Access"
	case "automation", "appleevents":
		return "Automation"
	}
	return scope
}

// scopeVerifier returns a (probe, name) for the given binary+scope pair, or
// (nil, "") when we don't know how to check this combination programmatically.
// Probes are scope-specific because TCC.db is SIP-protected; we lean on each
// tool's own self-report (`guiport doctor`, `um doctor`, etc).
func scopeVerifier(scope, binary string) (func(ctx context.Context) bool, string) {
	scope = strings.ReplaceAll(strings.ToLower(scope), "_", "-")
	if scope == "sr" {
		scope = "screen-recording"
	}
	if scope == "a11y" || scope == "ax" {
		scope = "accessibility"
	}
	switch binary {
	case "guiport":
		if scope == "screen-recording" {
			return guiportScreenRecordingVerifier(), "guiport.app screenshot"
		}
		// guiport doctor emits a line per scope with ✓/✗. Grep for the
		// scope we asked about; trusted = green.
		needle := "screen_recording: trusted"
		switch scope {
		case "accessibility":
			needle = "accessibility: trusted"
		case "input-monitoring":
			needle = "input_monitoring: trusted"
		case "screen-recording":
			needle = "screen_recording: trusted"
		default:
			return nil, ""
		}
		return func(ctx context.Context) bool {
			out, _ := exec.CommandContext(ctx, "guiport", "doctor").CombinedOutput()
			return strings.Contains(string(out), needle)
		}, "guiport doctor"
	case "um":
		// um doctor emits JSON; cheap substring is enough.
		needle := `"screenRecording":true`
		switch scope {
		case "accessibility":
			needle = `"accessibility":true`
		case "input-monitoring":
			needle = `"inputMonitoring":true`
		case "screen-recording":
			needle = `"screenRecording":true`
		default:
			return nil, ""
		}
		return func(ctx context.Context) bool {
			out, _ := exec.CommandContext(ctx, "um", "doctor").CombinedOutput()
			return strings.Contains(string(out), needle)
		}, "um doctor"
	}
	return nil, ""
}

func guiportScreenRecordingVerifier() func(ctx context.Context) bool {
	return func(ctx context.Context) bool {
		app := firstExistingDir(
			os.Getenv("VMLAB_GUIPORT_APP"),
			"/Applications/guiport.app",
			filepath.Join(os.Getenv("HOME"), "Applications", "guiport.app"),
		)
		if app == "" {
			out, _ := exec.CommandContext(ctx, "guiport", "doctor").CombinedOutput()
			return strings.Contains(string(out), "screen_recording: trusted")
		}
		f, err := os.CreateTemp("", "vmlab-guiport-sr-*.png")
		if err != nil {
			return false
		}
		path := f.Name()
		_ = f.Close()
		_ = os.Remove(path)
		defer os.Remove(path)

		cmd := exec.CommandContext(ctx, "open", "-n", "-W", "-gj", "-a", app, "--args", "screenshot", "--out", path)
		if err := cmd.Run(); err != nil {
			return false
		}
		info, err := os.Stat(path)
		return err == nil && info.Size() > 0
	}
}

func firstExistingDir(paths ...string) string {
	for _, path := range paths {
		switch path {
		case "", "off", "none", "0":
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}
	return ""
}
