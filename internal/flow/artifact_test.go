package flow

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// fakeTransport records Sync calls so tests can assert on delivery.
type fakeTransport struct {
	syncs []struct {
		Target target.Target
		Src    string
	}
}

func (f *fakeTransport) Name() string             { return "fake" }
func (f *fakeTransport) Capabilities() transport.Caps { return transport.Caps{Sync: true} }
func (f *fakeTransport) Doctor(context.Context, target.Target) transport.Health {
	return transport.Health{OK: true}
}
func (f *fakeTransport) Sync(_ context.Context, t target.Target, src string) error {
	f.syncs = append(f.syncs, struct {
		Target target.Target
		Src    string
	}{t, src})
	return nil
}
func (f *fakeTransport) Run(context.Context, target.Target, []string, io.Writer, io.Writer) (transport.Result, error) {
	return transport.Result{}, nil
}
func (f *fakeTransport) Shell(context.Context, target.Target) error { return nil }
func (f *fakeTransport) Screenshot(context.Context, target.Target, string) error {
	return nil
}
func (f *fakeTransport) GUI(context.Context, target.Target, transport.GUIAction) error {
	return nil
}

func TestHashSourceTreeStableAndChangeSensitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := hashSourceTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashSourceTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash not stable: %s != %s", h1, h2)
	}
	// Modify mtime.
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(filepath.Join(dir, "a.go"), future, future); err != nil {
		t.Fatal(err)
	}
	h3, _ := hashSourceTree(dir)
	if h3 == h1 {
		t.Errorf("hash should flip on mtime change")
	}
}

func TestHashSourceTreeEmptyAllowed(t *testing.T) {
	h, err := hashSourceTree("")
	if err != nil {
		t.Fatal(err)
	}
	if h != "" {
		t.Errorf("empty src should yield empty hash, got %q", h)
	}
}

func TestArtifactCacheKeyChangesWithInputs(t *testing.T) {
	k1 := artifactCacheKey("abc", "go build", "linux", "amd64")
	k2 := artifactCacheKey("abc", "go build", "linux", "arm64")
	k3 := artifactCacheKey("def", "go build", "linux", "amd64")
	k4 := artifactCacheKey("abc", "go build -v", "linux", "amd64")
	if k1 == k2 || k1 == k3 || k1 == k4 {
		t.Errorf("cache keys should differ when any input changes")
	}
	k5 := artifactCacheKey("abc", "go build", "linux", "amd64")
	if k1 != k5 {
		t.Errorf("identical inputs must yield identical key")
	}
}

func TestRunArtifactStepCachesAcrossCalls(t *testing.T) {
	// Skip if `sh` not available (tests assume a POSIX host).
	if runtimeIsWindowsHost() {
		t.Skip("test assumes POSIX host")
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "marker.txt")

	spec := &ArtifactSpec{
		Src: srcDir,
		Build: map[string]string{
			// Write a marker each build so we can count invocations.
			"linux": "echo built >> " + outFile,
		},
	}

	// First call: cache miss, build runs.
	var out, errb bytes.Buffer
	_, cached, err := runArtifactStep(context.Background(), spec, "linux", "amd64", cacheDir, nil, target.Target{}, &out, &errb)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if cached {
		t.Fatal("first call must be a cache miss")
	}
	data1, _ := os.ReadFile(outFile)
	if !strings.Contains(string(data1), "built") {
		t.Fatalf("build script didn't run on miss: %q", data1)
	}

	// Second call (same inputs): cache hit, build does NOT run again.
	out.Reset()
	errb.Reset()
	_, cached, err = runArtifactStep(context.Background(), spec, "linux", "amd64", cacheDir, nil, target.Target{}, &out, &errb)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !cached {
		t.Fatal("second call must be a cache hit")
	}
	data2, _ := os.ReadFile(outFile)
	if string(data2) != string(data1) {
		t.Errorf("cache hit must skip the build script; got %q vs %q", data2, data1)
	}

	// Touch source → cache invalidated, build runs again.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(srcDir, "main.go"), future, future); err != nil {
		t.Fatal(err)
	}
	_, cached, err = runArtifactStep(context.Background(), spec, "linux", "amd64", cacheDir, nil, target.Target{}, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Fatal("source change must invalidate the cache")
	}
}

func TestRunArtifactStepUnknownOSReturnsMissingEntry(t *testing.T) {
	spec := &ArtifactSpec{
		Build: map[string]string{"linux": "true"},
	}
	cmd, cached, err := runArtifactStep(context.Background(), spec, "windows", "amd64", "", nil, target.Target{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "" {
		t.Errorf("expected empty cmd for unknown OS, got %q", cmd)
	}
	if cached {
		t.Errorf("unknown OS should not be a cache hit")
	}
}

func TestRunArtifactStepDeliversOutputToTarget(t *testing.T) {
	if runtimeIsWindowsHost() {
		t.Skip("test assumes POSIX host")
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "myapp-linux")

	spec := &ArtifactSpec{
		Src: srcDir,
		Build: map[string]string{
			"linux": "printf binary > " + outPath,
		},
		Output: map[string]string{
			"linux": outPath,
		},
		DeliverTo: "/opt/myapp/",
	}

	ft := &fakeTransport{}
	tgt := target.Target{
		Name:      "linux-vm",
		Transport: "ssh",
		Settings: map[string]any{
			"ssh": map[string]any{"host": "vm.lan", "user": "edi"},
		},
	}

	cmd, cached, err := runArtifactStep(context.Background(), spec, "linux", "amd64", "", ft, tgt, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("step failed: %v", err)
	}
	if cached {
		t.Fatal("first call should be a miss")
	}
	if cmd == "" {
		t.Fatal("expected non-empty build cmd")
	}

	if len(ft.syncs) != 1 {
		t.Fatalf("expected exactly one Sync call, got %d", len(ft.syncs))
	}
	got := ft.syncs[0]
	if got.Src != outPath {
		t.Errorf("Sync src = %q, want %q", got.Src, outPath)
	}
	dest := got.Target.SettingString("ssh", "dest")
	if dest != "/opt/myapp/" {
		t.Errorf("delivered target ssh.dest = %q, want %q", dest, "/opt/myapp/")
	}
	// Original target must remain unmutated.
	if tgt.SettingString("ssh", "dest") != "" {
		t.Errorf("original target was mutated: ssh.dest=%q", tgt.SettingString("ssh", "dest"))
	}
}

func TestRunArtifactStepNoDeliveryWhenDeliverToEmpty(t *testing.T) {
	if runtimeIsWindowsHost() {
		t.Skip("test assumes POSIX host")
	}
	srcDir := t.TempDir()
	outPath := filepath.Join(t.TempDir(), "marker")
	spec := &ArtifactSpec{
		Src: srcDir,
		Build: map[string]string{
			"linux": "printf x > " + outPath,
		},
		Output: map[string]string{"linux": outPath},
		// DeliverTo intentionally empty
	}
	ft := &fakeTransport{}
	tgt := target.Target{Name: "x", Transport: "ssh"}
	if _, _, err := runArtifactStep(context.Background(), spec, "linux", "amd64", "", ft, tgt, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(ft.syncs) != 0 {
		t.Errorf("expected zero syncs when DeliverTo is empty, got %d", len(ft.syncs))
	}
}

func TestRunArtifactStepMissingOutputFails(t *testing.T) {
	if runtimeIsWindowsHost() {
		t.Skip("test assumes POSIX host")
	}
	srcDir := t.TempDir()
	spec := &ArtifactSpec{
		Src:       srcDir,
		Build:     map[string]string{"linux": "true"},                                     // succeeds but produces nothing
		Output:    map[string]string{"linux": filepath.Join(t.TempDir(), "never-built")}, // path doesn't exist
		DeliverTo: "/opt/x/",
	}
	ft := &fakeTransport{}
	tgt := target.Target{Name: "x", Transport: "ssh"}
	_, _, err := runArtifactStep(context.Background(), spec, "linux", "amd64", "", ft, tgt, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when output is missing")
	}
	if !strings.Contains(err.Error(), "missing after build") {
		t.Errorf("unexpected error: %v", err)
	}
}
