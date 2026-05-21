package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MatrixRow is one compact result line emitted by `--format=matrix`.
// One row per target (or per instance, in the fleet/@@ case). On failure
// the row carries a capped stderr tail so an agent can decide how to
// react without pulling the full evidence bundle. Encoded as newline-
// delimited JSON so a watcher / pager can consume it as a stream.
type MatrixRow struct {
	Target     string `json:"target"`
	Transport  string `json:"transport,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Step       string `json:"step,omitempty"`
	Status     string `json:"status"` // pass | fail
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Tail       string `json:"tail,omitempty"`
}

// matrixTailLines caps how much stderr we attach to a failing row.
// Bounded so agents can react cheaply without us writing the full log
// into context. 40 lines covers a typical Go/Rust panic plus a small
// surrounding window.
const matrixTailLines = 40

// emitMatrix writes one JSON object per row, newline-delimited, to w.
func emitMatrix(w io.Writer, rows []MatrixRow) error {
	enc := json.NewEncoder(w)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// tailStderr returns the last matrixTailLines of the per-target stderr.log
// the evidence run writes to <runDir>/targets/<name>/stderr.log. Returns
// "" if the file is missing or empty so the row stays compact when there
// is no log to attach.
func tailStderr(runDir, targetName string) string {
	if runDir == "" || targetName == "" {
		return ""
	}
	path := filepath.Join(runDir, "targets", sanitizeForFS(targetName), "stderr.log")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Ring buffer: cheaper than reading the whole file when logs grow.
	buf := make([]string, 0, matrixTailLines)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(buf) < matrixTailLines {
			buf = append(buf, line)
		} else {
			copy(buf, buf[1:])
			buf[matrixTailLines-1] = line
		}
	}
	if len(buf) == 0 {
		return ""
	}
	return strings.Join(buf, "\n")
}

// sanitizeForFS mirrors internal/evidence.sanitize (strings.NewReplacer
// "/", "_", "\\", "_", " ", "_"). Kept in lock-step so the tail lookup
// resolves the same on-disk path the evidence run wrote to.
func sanitizeForFS(s string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer("/", "_", "\\", "_", " ", "_")
	return r.Replace(s)
}

// isMatrixFormat returns true when the user asked for newline-delimited
// matrix rows. Case-insensitive so `--format=Matrix` etc. work too.
func isMatrixFormat(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "matrix")
}
