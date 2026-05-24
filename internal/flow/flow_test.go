package flow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

func TestLoadAndRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.yaml")
	body := `name: smoke
steps:
  - run: echo hi
  - assert: 'true'
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Steps) != 2 {
		t.Fatalf("steps: %d", len(f.Steps))
	}

	tr := transport.NewLocal()
	var out, errb bytes.Buffer
	steps, err := Run(context.Background(), tr, target.Target{Name: "local", Transport: "local"}, f, &out, &errb)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, errb.String())
	}
	if len(steps) != 2 {
		t.Fatalf("step results: %d", len(steps))
	}
	for _, s := range steps {
		if s.ExitCode != 0 {
			t.Fatalf("step %d exit %d", s.Index, s.ExitCode)
		}
	}
	if out.String() == "" {
		t.Fatal("no stdout captured")
	}
	_ = io.EOF
}

func TestExecStepPassesArgvDirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.yaml")
	body := `name: smoke
steps:
  - name: argv
    exec: ["sh", "-c", "echo argv-mode"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Steps) != 1 || len(f.Steps[0].Exec) != 3 {
		t.Fatalf("exec parse: %+v", f.Steps)
	}

	tr := transport.NewLocal()
	var out, errb bytes.Buffer
	steps, err := Run(context.Background(), tr, target.Target{Name: "local", Transport: "local"}, f, &out, &errb)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, errb.String())
	}
	if len(steps) != 1 || steps[0].ExitCode != 0 || steps[0].Kind != "exec" {
		t.Fatalf("expected one successful exec step, got %+v", steps)
	}
	if !bytes.Contains(out.Bytes(), []byte("argv-mode")) {
		t.Fatalf("expected argv-mode in stdout, got %q", out.String())
	}
}

func TestGUIStepDispatchesToGuiport(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("uses POSIX stub binaries")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "guiport.args")
	stub := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexit 0\n", argsFile)
	if err := os.WriteFile(filepath.Join(dir, "guiport"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	flowPath := filepath.Join(dir, "f.yaml")
	body := `name: gui-smoke
steps:
  - name: open
    gui:
      kind: hotkey
      text: "cmd+space"
  - gui:
      kind: type
      text: "vmlab $VMLAB_TARGET"
  - gui:
      kind: screenshot
      path: /tmp/shot.png
`
	if err := os.WriteFile(flowPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(flowPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(f.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(f.Steps))
	}
	if f.Steps[0].GUI == nil || f.Steps[0].GUI.Kind != "hotkey" {
		t.Fatalf("expected hotkey gui step, got %+v", f.Steps[0])
	}

	tgt := target.Target{
		Name:      "mac-local",
		Transport: "guiport",
		Settings:  map[string]any{"guiport": map[string]any{"app": "Calculator"}},
	}
	tr := transport.NewGuiport()
	var out, errb bytes.Buffer
	steps, err := Run(context.Background(), tr, tgt, f, &out, &errb)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, errb.String())
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(steps))
	}
	for _, s := range steps {
		if s.ExitCode != 0 || !strings.HasPrefix(s.Kind, "gui:") {
			t.Fatalf("unexpected step result %+v", s)
		}
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"hotkey cmd+space",
		"type vmlab mac-local",
		"screenshot --out /tmp/shot.png --app Calculator",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in guiport argv log, got:\n%s", want, got)
		}
	}
}

func TestGUIStepMissingKindFails(t *testing.T) {
	dir := t.TempDir()
	flowPath := filepath.Join(dir, "f.yaml")
	body := `name: bad
steps:
  - gui:
      selector: AXButton[title=Save]
`
	if err := os.WriteFile(flowPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flowPath); err == nil {
		t.Fatal("expected schema validation to reject gui without kind")
	}
}
