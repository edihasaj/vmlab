// Package evidence captures per-run artifacts under ~/.vmlab/runs/<run-id>/.
package evidence

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunMeta is the top-level summary written to meta.json.
type RunMeta struct {
	ID         string            `json:"id"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt,omitempty"`
	DurationMs int64             `json:"durationMs"`
	Targets    []TargetSummary   `json:"targets"`
	Cmd        string            `json:"cmd,omitempty"`
	Flow       string            `json:"flow,omitempty"`
	Selector   string            `json:"selector,omitempty"`
	ExitCode   int               `json:"exitCode"`
	Lifecycle  *LifecycleSummary `json:"lifecycle,omitempty"`
}

// LifecycleSummary records provider-side bookkeeping for runs that brought an
// instance up and then disposed of it. Foundation for cost reporting: the
// (UpMs + RunMs + DownMs) window approximates the billable uptime window.
type LifecycleSummary struct {
	Instance   string `json:"instance"`
	Provider   string `json:"provider"`
	PriorState string `json:"priorState,omitempty"`
	Changed    bool   `json:"changed"`
	Reason     string `json:"reason,omitempty"`
	Dispose    string `json:"dispose,omitempty"`
	UpMs       int64  `json:"upMs,omitempty"`
	RunMs      int64  `json:"runMs,omitempty"`
	DownMs     int64  `json:"downMs,omitempty"`
	Error      string `json:"error,omitempty"`
}

// TargetSummary is one target's slice of a run.
type TargetSummary struct {
	Name       string `json:"name"`
	Transport  string `json:"transport"`
	ExitCode   int    `json:"exitCode"`
	Error      string `json:"error,omitempty"`
	Duration   int64  `json:"durationMs"`
	StdoutPath string `json:"stdout,omitempty"`
	StderrPath string `json:"stderr,omitempty"`
	StepsPath  string `json:"steps,omitempty"`
}

// Run is a writable handle to an evidence bundle.
type Run struct {
	ID   string
	Dir  string
	meta RunMeta
}

// New creates a fresh run dir under root. Honours $VMLAB_RUN_ID when set so
// a caller (today: MCP background mode) can pre-allocate an ID, return it to
// the agent, and trust the spawned subprocess to land in the same evidence
// directory. The agent then polls evidence by that ID without racing for
// the "most recent" run.
func New(root string) (*Run, error) {
	id := os.Getenv("VMLAB_RUN_ID")
	if id == "" {
		id = newID()
	}
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Run{
		ID:  id,
		Dir: dir,
		meta: RunMeta{
			ID:        id,
			StartedAt: time.Now().UTC(),
		},
	}, nil
}

// SetCmd records the original command/flow string for the run.
func (r *Run) SetCmd(cmd string)    { r.meta.Cmd = cmd }
func (r *Run) SetFlow(path string)  { r.meta.Flow = path }
func (r *Run) SetSelector(s string) { r.meta.Selector = s }

// SetLifecycle attaches provider-lifecycle bookkeeping. Idempotent.
func (r *Run) SetLifecycle(l LifecycleSummary) { r.meta.Lifecycle = &l }

// WriteFile writes name (no path separators allowed) under the run dir.
// Used for plain-text snapshots like status-before.txt.
func (r *Run) WriteFile(name string, data []byte) error {
	return os.WriteFile(filepath.Join(r.Dir, sanitize(name)), data, 0o644)
}

// TargetWriters returns paired io.Writers for stdout+stderr that tee to the
// caller-supplied passthrough writers and to per-target log files. The returned
// closer should be deferred.
func (r *Run) TargetWriters(targetName string, stdout, stderr io.Writer) (io.Writer, io.Writer, *TargetLogs, error) {
	dir := filepath.Join(r.Dir, "targets", sanitize(targetName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, nil, err
	}
	outF, err := os.Create(filepath.Join(dir, "stdout.log"))
	if err != nil {
		return nil, nil, nil, err
	}
	errF, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		outF.Close()
		return nil, nil, nil, err
	}
	logs := &TargetLogs{
		Dir:    dir,
		Stdout: outF,
		Stderr: errF,
	}
	return io.MultiWriter(outF, stdout), io.MultiWriter(errF, stderr), logs, nil
}

// TargetLogs holds open file handles for one target's logs.
type TargetLogs struct {
	Dir    string
	Stdout *os.File
	Stderr *os.File
}

// Close flushes/closes all logs.
func (l *TargetLogs) Close() error {
	if l == nil {
		return nil
	}
	var firstErr error
	for _, f := range []*os.File{l.Stdout, l.Stderr} {
		if f == nil {
			continue
		}
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WriteSteps writes the step-by-step JSON record for a target.
func (r *Run) WriteSteps(targetName string, steps any) (string, error) {
	dir := filepath.Join(r.Dir, "targets", sanitize(targetName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "steps.json")
	data, err := json.MarshalIndent(steps, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// AddTarget appends a TargetSummary to the run.
func (r *Run) AddTarget(s TargetSummary) {
	r.meta.Targets = append(r.meta.Targets, s)
}

// Finish writes meta.json and returns the final RunMeta. Also removes the
// running.lock marker so `vmlab attach` and `vmlab cancel` consider the run
// complete.
func (r *Run) Finish(exitCode int) (RunMeta, error) {
	r.meta.FinishedAt = time.Now().UTC()
	r.meta.DurationMs = r.meta.FinishedAt.Sub(r.meta.StartedAt).Milliseconds()
	r.meta.ExitCode = exitCode
	// Sort targets by name for deterministic reads.
	sort.Slice(r.meta.Targets, func(i, j int) bool {
		return r.meta.Targets[i].Name < r.meta.Targets[j].Name
	})
	data, err := json.MarshalIndent(r.meta, "", "  ")
	if err != nil {
		return r.meta, err
	}
	_ = os.Remove(filepath.Join(r.Dir, "running.lock"))
	return r.meta, os.WriteFile(filepath.Join(r.Dir, "meta.json"), data, 0o644)
}

// MarkRunning writes a running.lock file with the current process PID into
// the run dir. Used by lifecycle-wrapped CLI commands (`with`, `run @<inst>`,
// `run @@<class>`, `image build`) so `vmlab attach <run-id>` and
// `vmlab cancel <run-id>` can find the live process. Best-effort — failures
// are logged but never block the run.
func (r *Run) MarkRunning() error {
	pid := os.Getpid()
	state := map[string]any{
		"pid":     pid,
		"started": r.meta.StartedAt,
		"cmd":     r.meta.Cmd,
		"flow":    r.meta.Flow,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.Dir, "running.lock"), data, 0o644)
}

// RunningState is the parsed shape of running.lock.
type RunningState struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Cmd     string    `json:"cmd,omitempty"`
	Flow    string    `json:"flow,omitempty"`
}

// ReadRunningState returns the running.lock contents for runDir or an error
// (os.ErrNotExist when the file isn't present — run has finished).
func ReadRunningState(runDir string) (RunningState, error) {
	var s RunningState
	data, err := os.ReadFile(filepath.Join(runDir, "running.lock"))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// Read loads a meta.json from a run directory.
func Read(runDir string) (RunMeta, error) {
	var m RunMeta
	data, err := os.ReadFile(filepath.Join(runDir, "meta.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

// List returns runs under root, newest first.
func List(root string) ([]RunMeta, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []RunMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := Read(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// RunStatus is a snapshot of a run: still running, what the per-target
// stats look like so far, exit code if finished. Returned by Status() so
// MCP / CLI callers can poll without re-parsing meta.json every time.
type RunStatus struct {
	ID         string          `json:"id"`
	Running    bool            `json:"running"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	DurationMs int64           `json:"durationMs"`
	ExitCode   *int            `json:"exitCode,omitempty"`
	Targets    []TargetSummary `json:"targets,omitempty"`
	Cmd        string          `json:"cmd,omitempty"`
	Flow       string          `json:"flow,omitempty"`
	Selector   string          `json:"selector,omitempty"`
	HolderPID  int             `json:"holderPid,omitempty"`
}

// Status returns a snapshot for the run at runDir. Running is determined
// by the presence of running.lock (set by MarkRunning, cleared by Finish);
// finished runs return their final meta.json verbatim.
func Status(runDir string) (RunStatus, error) {
	out := RunStatus{}
	st, runErr := ReadRunningState(runDir)
	running := runErr == nil
	meta, metaErr := Read(runDir)
	if metaErr != nil && !running {
		return out, metaErr
	}
	out.ID = meta.ID
	out.StartedAt = meta.StartedAt
	out.Cmd = meta.Cmd
	out.Flow = meta.Flow
	out.Selector = meta.Selector
	out.Targets = meta.Targets
	out.Running = running
	if running {
		out.HolderPID = st.PID
		if !st.Started.IsZero() && out.StartedAt.IsZero() {
			out.StartedAt = st.Started
		}
		return out, nil
	}
	// Finished — meta.json reflects the final state.
	out.FinishedAt = &meta.FinishedAt
	out.DurationMs = meta.DurationMs
	ec := meta.ExitCode
	out.ExitCode = &ec
	return out, nil
}

// LogCursor encodes per-target byte offsets so a streaming caller can
// resume mid-file across multiple ReadLogChunk calls. Wire format is a
// compact comma list: `<target>:<offset>[,<target>:<offset>…]`.
//
// stream is appended in the offset key (e.g. `lin:stdout:12345`) so the
// two log streams are tracked independently. Cursors are opaque to the
// agent — vmlab parses them; agents just echo what they got back.
type LogCursor map[string]int64

// ParseLogCursor decodes the wire string. An empty input returns an empty
// cursor (start of every file).
func ParseLogCursor(s string) LogCursor {
	out := LogCursor{}
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		i := strings.LastIndex(part, ":")
		if i <= 0 {
			continue
		}
		var off int64
		fmt.Sscanf(part[i+1:], "%d", &off)
		out[part[:i]] = off
	}
	return out
}

// String emits the cursor in the wire format. Sorted so the same set of
// offsets always produces the same string (helps with idempotency tests).
func (c LogCursor) String() string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, c[k]))
	}
	return strings.Join(parts, ",")
}

// LogChunk is one batch of new bytes pulled from a target's log file.
type LogChunk struct {
	Target string `json:"target"`
	Stream string `json:"stream"` // "stdout" | "stderr"
	Bytes  string `json:"bytes"`
	Offset int64  `json:"offset"` // post-read offset for this (target, stream)
}

// ReadLogChunks slices new bytes from every (target, stream) pair under
// runDir/targets/, starting at the offsets in cursor. Returns the chunks
// plus an updated cursor. maxBytesPerStream caps the per-stream slice so
// a noisy log doesn't return a megabyte in one MCP call (0 = no cap).
//
// When targetFilter is non-empty, only that target is read — useful for
// agents that want one target's stream at a time.
func ReadLogChunks(runDir string, cursor LogCursor, targetFilter string, maxBytesPerStream int) ([]LogChunk, LogCursor, error) {
	targetsDir := filepath.Join(runDir, "targets")
	entries, err := os.ReadDir(targetsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, cursor, nil
		}
		return nil, cursor, err
	}
	next := LogCursor{}
	for k, v := range cursor {
		next[k] = v
	}
	var chunks []LogChunk
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if targetFilter != "" && name != targetFilter {
			continue
		}
		for _, stream := range []string{"stdout", "stderr"} {
			key := name + ":" + stream
			path := filepath.Join(targetsDir, name, stream+".log")
			data, off, err := readSince(path, cursor[key], maxBytesPerStream)
			if err != nil {
				continue
			}
			next[key] = off
			if len(data) > 0 {
				chunks = append(chunks, LogChunk{
					Target: name,
					Stream: stream,
					Bytes:  string(data),
					Offset: off,
				})
			}
		}
	}
	return chunks, next, nil
}

// readSince opens path, seeks to from, and reads up to maxBytes (0 = all).
// Returns the slurped bytes plus the new offset. Missing file is treated
// as empty — the target's log may not exist yet for early polling.
func readSince(path string, from int64, maxBytes int) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, from, nil
		}
		return nil, from, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, from, err
	}
	size := st.Size()
	if from > size {
		// File rotated/truncated underneath us — reset to start.
		from = 0
	}
	if from >= size {
		return nil, size, nil
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from, err
	}
	toRead := size - from
	if maxBytes > 0 && toRead > int64(maxBytes) {
		toRead = int64(maxBytes)
	}
	buf := make([]byte, toRead)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, from, err
	}
	return buf[:n], from + int64(n), nil
}

// Bundle creates a zip of the run dir at outZip.
func Bundle(runDir, outZip string) error {
	f, err := os.Create(outZip)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()
	return filepath.Walk(runDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(runDir, path)
		if err != nil {
			return err
		}
		zw, err := w.Create(rel)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(zw, src)
		return err
	})
}

// Step is the on-disk shape of one flow step (mirrors flow.StepResult).
type Step struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	Cmd        string `json:"cmd"`
	Name       string `json:"name,omitempty"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

func readSteps(path string) ([]Step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Step
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// dirSize returns the total bytes occupied by a single run directory.
func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// runInfo bundles a run's path, mtime, and size — used by PruneToFitSize so
// we read each dir once and sort by age in memory.
type runInfo struct {
	dir       string
	startedAt time.Time
	size      int64
}

// PruneToFitSize keeps deleting oldest runs until the total size of `root`
// is at most maxBytes. Pass 0 to skip the size check. Returns the number
// of run directories deleted.
//
// Combined with PruneOlderThan in `vmlab evidence prune --auto` so the
// retention policy honours both age and size:
//
//  1. age first (gets rid of definitely-stale)
//  2. size second (keeps the dir under cap even when traffic spikes)
func PruneToFitSize(root string, maxBytes int64) (int, error) {
	if maxBytes <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	runs := make([]runInfo, 0, len(entries))
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := Read(dir)
		if err != nil {
			continue
		}
		sz, _ := dirSize(dir)
		total += sz
		runs = append(runs, runInfo{dir: dir, startedAt: m.StartedAt, size: sz})
	}
	if total <= maxBytes {
		return 0, nil
	}
	// Oldest first — we trim from the head.
	sort.Slice(runs, func(i, j int) bool { return runs[i].startedAt.Before(runs[j].startedAt) })
	deleted := 0
	for _, r := range runs {
		if total <= maxBytes {
			break
		}
		if err := os.RemoveAll(r.dir); err == nil {
			total -= r.size
			deleted++
		}
	}
	return deleted, nil
}

// PruneOlderThan removes runs older than the cutoff.
func PruneOlderThan(root string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := Read(dir)
		if err != nil {
			continue
		}
		if m.StartedAt.Before(cutoff) {
			if err := os.RemoveAll(dir); err == nil {
				n++
			}
		}
	}
	return n, nil
}

func newID() string {
	t := time.Now().UTC().Format("20060102T150405")
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return t
	}
	return fmt.Sprintf("%s-%s", t, hex.EncodeToString(b[:]))
}

var sanitizeRepl = strings.NewReplacer("/", "_", "\\", "_", " ", "_")

func sanitize(s string) string { return sanitizeRepl.Replace(s) }
