package cli

import (
	"context"
	"fmt"
	"os/exec"
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
			fmt.Fprintf(w, "Opening System Settings → Privacy & Security → %s\n", prettyScope(scope))
			fmt.Fprintf(w, "Find '%s' in the list and toggle it ON.\n", binary)
			fmt.Fprintf(w, "macOS will prompt for Touch ID / admin password — that's the one gesture we can't automate.\n\n")

			if err := exec.Command("open", url).Run(); err != nil {
				return fmt.Errorf("open settings: %w", err)
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
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "max time to wait for the grant")
	return c
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
