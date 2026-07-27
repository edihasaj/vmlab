package transport

import (
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

func TestSSHMultiplexConfigDisabledOnWindows(t *testing.T) {
	t.Setenv("VMLAB_HOME", t.TempDir())

	dir, persist, ok := sshMultiplexConfigForOS(target.Target{}, "windows")

	if ok || dir != "" || persist != "" {
		t.Fatalf("sshMultiplexConfigForOS(windows) = (%q, %q, %v), want disabled", dir, persist, ok)
	}
}
