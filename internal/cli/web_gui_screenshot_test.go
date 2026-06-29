package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGUICmdAcceptsAnyGUICapableTransport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")
	targetDir := filepath.Join(home, ".vmlab", "targets")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`name: remote-mac
transport: ssh-mac
ssh:
  host: 127.0.0.1
`)
	if err := os.WriteFile(filepath.Join(targetDir, "remote-mac.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRoot()
	cmd.SetArgs([]string{"gui", "remote-mac", "--kind", "wait", "--ms", "1"})
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("gui wait should accept ssh-mac transport: %v\nstderr=%s", err, errOut.String())
	}
}

func TestGUICmdRejectsNonGUICapableTransport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VMLAB_HOME", "")
	targetDir := filepath.Join(home, ".vmlab", "targets")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`name: plain-ssh
transport: ssh
ssh:
  host: 127.0.0.1
`)
	if err := os.WriteFile(filepath.Join(targetDir, "plain-ssh.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRoot()
	cmd.SetArgs([]string{"gui", "plain-ssh", "--kind", "wait", "--ms", "1"})
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-GUI transport to be rejected")
	}
	if !strings.Contains(err.Error(), "does not support gui actions") {
		t.Fatalf("unexpected error: %v", err)
	}
}
