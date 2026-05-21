package flow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	_, cached, err := runArtifactStep(context.Background(), spec, "linux", "amd64", cacheDir, &out, &errb)
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
	_, cached, err = runArtifactStep(context.Background(), spec, "linux", "amd64", cacheDir, &out, &errb)
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
	_, cached, err = runArtifactStep(context.Background(), spec, "linux", "amd64", cacheDir, &out, &errb)
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
	cmd, cached, err := runArtifactStep(context.Background(), spec, "windows", "amd64", "", &bytes.Buffer{}, &bytes.Buffer{})
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
