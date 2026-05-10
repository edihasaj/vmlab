// Package logging wires a process-wide slog handler. Defaults to a
// human-readable text handler on stderr at INFO; --verbose drops to DEBUG.
package logging

import (
	"io"
	"log/slog"
	"os"
)

// Setup installs the default logger. verbose enables DEBUG. dst defaults to
// stderr when nil.
func Setup(verbose bool, dst io.Writer) {
	if dst == nil {
		dst = os.Stderr
	}
	lvl := slog.LevelInfo
	if verbose {
		lvl = slog.LevelDebug
	}
	h := slog.NewTextHandler(dst, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Drop the noisy default timestamp for CLI use.
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(h))
}
