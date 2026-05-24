package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

func newWebCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "web <target> -- <abx-args>...",
		Short: "Run an abx-style web action against a web target",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, tr, err := lookupTarget(args[0], "abx")
			if err != nil {
				return err
			}
			rest := stripDashDash(args[1:])
			res, err := tr.Run(cmd.Context(), t, rest, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("abx exited %d", res.ExitCode)
			}
			return nil
		},
	}
	return c
}

func newGUICmd() *cobra.Command {
	var (
		kind     string
		selector string
		text     string
		path     string
		x        int
		y        int
		ms       int
	)
	c := &cobra.Command{
		Use:   "gui <target>",
		Short: "Run a guiport-style desktop UI action",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, tr, err := lookupTarget(args[0], "guiport")
			if err != nil {
				return err
			}
			a := transport.GUIAction{Kind: kind, Selector: selector, Text: text, Path: path}
			if a.Kind == "" {
				return fmt.Errorf("--kind is required")
			}
			extra := map[string]any{}
			if cmd.Flags().Changed("x") {
				extra["x"] = x
			}
			if cmd.Flags().Changed("y") {
				extra["y"] = y
			}
			if cmd.Flags().Changed("ms") {
				extra["milliseconds"] = ms
			}
			if len(extra) > 0 {
				a.Extra = extra
			}
			return tr.GUI(cmd.Context(), t, a)
		},
	}
	c.Flags().StringVar(&kind, "kind", "", "action kind: click | click-text | click-at | type | hotkey | screenshot | observe | tree | wait | run")
	c.Flags().StringVar(&selector, "selector", "", "AX selector or descriptor (or hotkey chord as fallback)")
	c.Flags().StringVar(&text, "text", "", "text to type, click-text target, or hotkey chord")
	c.Flags().StringVar(&path, "path", "", "screenshot output path or flow path")
	c.Flags().IntVar(&x, "x", 0, "x coord for click-at")
	c.Flags().IntVar(&y, "y", 0, "y coord for click-at")
	c.Flags().IntVar(&ms, "ms", 0, "milliseconds for wait")
	return c
}

func newScreenshotCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "screenshot <target> <out-path>",
		Short: "Capture a screenshot from a target that supports it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, tr, err := lookupTargetAny(args[0])
			if err != nil {
				return err
			}
			if err := tr.Screenshot(cmd.Context(), t, args[1]); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"target": t.Name, "path": args[1]})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", args[1])
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return c
}

// lookupTarget resolves a target by name and returns its transport, asserting
// the transport name matches expected (when non-empty).
func lookupTarget(name, expectedTransport string) (target.Target, transport.Transport, error) {
	t, tr, err := lookupTargetAny(name)
	if err != nil {
		return target.Target{}, nil, err
	}
	if expectedTransport != "" && t.Transport != expectedTransport {
		return t, nil, fmt.Errorf("target %s uses transport %q, expected %q", name, t.Transport, expectedTransport)
	}
	return t, tr, nil
}

func lookupTargetAny(name string) (target.Target, transport.Transport, error) {
	_, p, err := config.Load()
	if err != nil {
		return target.Target{}, nil, err
	}
	r, err := target.Load(p)
	if err != nil {
		return target.Target{}, nil, err
	}
	t, ok := r.Get(name)
	if !ok {
		return target.Target{}, nil, fmt.Errorf("unknown target: %s", name)
	}
	tr, err := transport.Default().Get(t.Transport)
	return t, tr, err
}

// (helper to keep imports happy; trims leading "--" so callers can split flags)
var _ = strings.TrimSpace
