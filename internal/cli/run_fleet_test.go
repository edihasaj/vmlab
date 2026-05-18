package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/edihasaj/vmlab/internal/config"
)

func TestPrefixWriterPrefixesEveryLine(t *testing.T) {
	var buf bytes.Buffer
	w := &prefixWriter{w: &buf, prefix: "[a] "}
	_, _ = w.Write([]byte("hello\nworld\n"))
	got := buf.String()
	want := "[a] hello\n[a] world\n"
	if got != want {
		t.Fatalf("prefixWriter:\n got=%q\nwant=%q", got, want)
	}
}

func TestPrefixWriterHandlesNoTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	w := &prefixWriter{w: &buf, prefix: "[x] "}
	_, _ = w.Write([]byte("first\nsecond"))
	got := buf.String()
	want := "[x] first\n[x] second"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestPrefixWriterContinuationAfterTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	w := &prefixWriter{w: &buf, prefix: "[i] "}
	_, _ = w.Write([]byte("one\n"))
	_, _ = w.Write([]byte("two\n"))
	got := buf.String()
	want := "[i] one\n[i] two\n"
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestInstanceClassShortcutMatchesTaggedInstances(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	instancesDir := filepath.Join(home, ".vmlab", "instances")
	if err := os.MkdirAll(instancesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeYAML(t, filepath.Join(instancesDir, "a.yaml"), `
name: lin-a
provider: hetzner
tags: [linux, smoke]
hetzner: {}
`)
	writeYAML(t, filepath.Join(instancesDir, "b.yaml"), `
name: lin-b
provider: hetzner
tags: [linux]
hetzner: {}
`)
	writeYAML(t, filepath.Join(instancesDir, "c.yaml"), `
name: win-a
provider: parallels
tags: [windows]
parallels: { vm: "Windows 11" }
`)

	_, paths, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	insts, ok := instanceClassShortcut("@@linux", paths)
	if !ok {
		t.Fatalf("expected linux match")
	}
	if len(insts) != 2 {
		t.Fatalf("want 2 linux instances, got %d: %v", len(insts), instanceNames(insts))
	}
	names := instanceNames(insts)
	found := map[string]bool{names[0]: true, names[1]: true}
	for _, want := range []string{"lin-a", "lin-b"} {
		if !found[want] {
			t.Errorf("missing %q in result %v", want, names)
		}
	}

	// @@windows should pick exactly one
	winInsts, ok := instanceClassShortcut("@@windows", paths)
	if !ok || len(winInsts) != 1 || winInsts[0].Name != "win-a" {
		t.Fatalf("@@windows: ok=%v %v", ok, instanceNames(winInsts))
	}

	// @@unknown finds no instances → reports (nil, false) so caller can try
	// the target selector path.
	if _, ok := instanceClassShortcut("@@unknown", paths); ok {
		t.Errorf("expected @@unknown to miss")
	}
}

func TestInstanceClassShortcutRejectsNonClass(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, paths, _ := config.Load()
	cases := []string{"@linux", "linux", "@@", "@@linux,smoke", ""}
	for _, c := range cases {
		if _, ok := instanceClassShortcut(c, paths); ok {
			t.Errorf("expected reject: %q", c)
		}
	}
}

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
