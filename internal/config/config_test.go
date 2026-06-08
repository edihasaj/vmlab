package config

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsHonorsVMLabHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("VMLAB_HOME", root)

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}

	if paths.UserDir != root {
		t.Fatalf("UserDir = %q, want %q", paths.UserDir, root)
	}
	if paths.UserFile != filepath.Join(root, "config.yaml") {
		t.Fatalf("UserFile = %q", paths.UserFile)
	}
	if paths.RunsDir != filepath.Join(root, "runs") {
		t.Fatalf("RunsDir = %q", paths.RunsDir)
	}
	if paths.StateDir != filepath.Join(root, "state") {
		t.Fatalf("StateDir = %q", paths.StateDir)
	}
}
