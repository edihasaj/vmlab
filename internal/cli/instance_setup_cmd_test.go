package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
)

func TestSetupName(t *testing.T) {
	cases := map[string]string{
		"Ubuntu 24.04.3 ARM64": "ubuntu-24-04-3-arm64",
		"  ":                   "linux-vm",
		"dev_linux":            "dev-linux",
	}
	for in, want := range cases {
		if got := setupName(in); got != want {
			t.Fatalf("setupName(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestWriteLinuxRepoConfig(t *testing.T) {
	dir := t.TempDir()
	files, err := writeLinuxRepoConfig(dir, "ubuntu", "10.211.55.7", "parallels", "/tmp/id", "Ubuntu", "/work/crabbox", false)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("files=%d, want 5", len(files))
	}
	for _, path := range []string{
		"vmlab/targets/ubuntu-ssh.yaml",
		"vmlab/targets/ubuntu-crabbox.yaml",
		"vmlab/targets/ubuntu-parallels.yaml",
		"vmlab/flows/ubuntu-smoke.yaml",
		"vmlab/flows/ubuntu-parallels-smoke.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "vmlab/targets/ubuntu-parallels.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"transport: parallels-guest", "vm: Ubuntu", "os: linux"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if _, err := writeLinuxRepoConfig(dir, "ubuntu", "10.211.55.7", "parallels", "/tmp/id", "Ubuntu", "/work/crabbox", false); err == nil {
		t.Fatal("expected existing file error")
	}
	reg, err := target.Load(config.Paths{TargetDir: []string{filepath.Join(dir, "vmlab/targets")}})
	if err != nil {
		t.Fatalf("load generated targets: %v", err)
	}
	if len(reg.All()) != 3 {
		t.Fatalf("generated targets=%d, want 3", len(reg.All()))
	}
}
