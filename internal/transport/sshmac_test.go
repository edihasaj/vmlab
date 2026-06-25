package transport

import (
	"strings"
	"testing"
)

// TestRemoteGuiportPrefixesPATH guards the fix for ssh-mac's false-negative
// doctor: a non-login ssh command shell does not source the user's profile, so
// a Homebrew-installed `guiport` (/opt/homebrew/bin on Apple Silicon,
// /usr/local/bin on Intel) was "command not found". The remote command must
// carry a PATH prefix covering both Homebrew prefixes.
func TestRemoteGuiportPrefixesPATH(t *testing.T) {
	s := &sshMacTransport{bin: "guiport"}
	got := s.remoteGuiport("doctor")
	if !strings.Contains(got, "/opt/homebrew/bin") || !strings.Contains(got, "/usr/local/bin") {
		t.Errorf("expected Homebrew bin dirs on PATH, got %q", got)
	}
	if !strings.HasSuffix(got, "guiport doctor") {
		t.Errorf("expected the guiport command preserved, got %q", got)
	}
}

// An absolute binary override is already pinned by the caller — no PATH prefix
// needed (and adding one would be misleading).
func TestRemoteGuiportAbsoluteBinSkipsPrefix(t *testing.T) {
	s := &sshMacTransport{bin: "/usr/local/bin/guiport"}
	got := s.remoteGuiport("screenshot --out /tmp/x.png")
	if strings.Contains(got, "PATH=") {
		t.Errorf("absolute bin should not get a PATH prefix, got %q", got)
	}
	if got != "/usr/local/bin/guiport screenshot --out /tmp/x.png" {
		t.Errorf("unexpected command: %q", got)
	}
}
