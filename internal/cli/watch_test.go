package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHashWatchSetStableAndChangeSensitive(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "sub", "b.go")
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1, err := hashWatchSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashWatchSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash should be stable: %s != %s", h1, h2)
	}

	// Bump mtime on b — hash must change.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(b, future, future); err != nil {
		t.Fatal(err)
	}
	h3, err := hashWatchSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h1 {
		t.Fatalf("hash should change after mtime bump")
	}

	// Append bytes — size change must also flip hash.
	if err := os.WriteFile(a, []byte("package x // more"), 0o644); err != nil {
		t.Fatal(err)
	}
	h4, err := hashWatchSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if h4 == h3 {
		t.Fatalf("hash should change after content edit")
	}
}

func TestHashWatchSetSkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	visible := filepath.Join(dir, "a.go")
	hidden := filepath.Join(dir, ".git", "HEAD")
	if err := os.MkdirAll(filepath.Dir(hidden), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visible, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidden, []byte("ref: refs/heads/main"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1, _ := hashWatchSet([]string{dir})

	// Modify the .git/HEAD — should not flip the hash.
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(hidden, future, future); err != nil {
		t.Fatal(err)
	}
	h2, _ := hashWatchSet([]string{dir})
	if h1 != h2 {
		t.Errorf("hidden-dir change must not flip the watch hash: %s vs %s", h1, h2)
	}

	// Modifying the visible file should flip it.
	if err := os.Chtimes(visible, future, future); err != nil {
		t.Fatal(err)
	}
	h3, _ := hashWatchSet([]string{dir})
	if h3 == h1 {
		t.Errorf("visible-file change must flip the watch hash")
	}
}

func TestHashWatchSetMissingPathIsStable(t *testing.T) {
	h1, err := hashWatchSet([]string{"/nope/does/not/exist"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashWatchSet([]string{"/nope/does/not/exist"})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("missing-path hash must be stable across calls")
	}
}

func TestIsLikelyFlowPath(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "smoke.yaml")
	if err := os.WriteFile(yamlPath, []byte("steps:\n  - run: true"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isLikelyFlowPath(yamlPath) {
		t.Errorf("expected %q to be a flow path", yamlPath)
	}
	if isLikelyFlowPath(filepath.Join(dir, "not-there.yaml")) {
		t.Errorf("missing file must not be treated as flow")
	}
	if isLikelyFlowPath("ls") {
		t.Errorf("plain command must not be treated as flow")
	}
}

func TestShortHashTruncates(t *testing.T) {
	if got := short("abcdef123456789"); got != "abcdef123456" {
		t.Errorf("expected first 12 chars, got %q", got)
	}
	if got := short("short"); got != "short" {
		t.Errorf("short input should pass through, got %q", got)
	}
	if !strings.HasPrefix("abcdef123456", short("abcdef123456789")) {
		t.Errorf("short should be a prefix")
	}
}
