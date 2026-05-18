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

// New creates a fresh run dir under root.
func New(root string) (*Run, error) {
	id := newID()
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
