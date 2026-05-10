package flow

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
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
