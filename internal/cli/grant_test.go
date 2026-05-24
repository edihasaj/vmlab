package cli

import (
	"strings"
	"testing"
)

func TestTCCScopeURL(t *testing.T) {
	cases := []struct {
		scope   string
		wantSub string
		wantErr bool
	}{
		{scope: "screen-recording", wantSub: "Privacy_ScreenCapture"},
		{scope: "screen_recording", wantSub: "Privacy_ScreenCapture"},
		{scope: "sr", wantSub: "Privacy_ScreenCapture"},
		{scope: "accessibility", wantSub: "Privacy_Accessibility"},
		{scope: "ax", wantSub: "Privacy_Accessibility"},
		{scope: "input-monitoring", wantSub: "Privacy_ListenEvent"},
		{scope: "full-disk-access", wantSub: "Privacy_AllFiles"},
		{scope: "automation", wantSub: "Privacy_Automation"},
		{scope: "camera", wantSub: "Privacy_Camera"},
		{scope: "microphone", wantSub: "Privacy_Microphone"},
		{scope: "made-up", wantErr: true},
	}
	for _, c := range cases {
		got, err := tccScopeURL(c.scope)
		if c.wantErr {
			if err == nil {
				t.Errorf("scope=%q expected error, got nil", c.scope)
			}
			continue
		}
		if err != nil {
			t.Errorf("scope=%q unexpected error: %v", c.scope, err)
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("scope=%q url=%q missing %q", c.scope, got, c.wantSub)
		}
		if !strings.HasPrefix(got, "x-apple.systempreferences:") {
			t.Errorf("scope=%q url should start with x-apple.systempreferences:, got %q", c.scope, got)
		}
	}
}

func TestPrettyScope(t *testing.T) {
	cases := map[string]string{
		"screen-recording": "Screen Recording",
		"sr":               "Screen Recording",
		"accessibility":    "Accessibility",
		"ax":               "Accessibility",
		"input-monitoring": "Input Monitoring",
		"full-disk-access": "Full Disk Access",
		"automation":       "Automation",
		"unknown":          "unknown",
	}
	for in, want := range cases {
		if got := prettyScope(in); got != want {
			t.Errorf("prettyScope(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestScopeVerifier(t *testing.T) {
	// Known combinations return non-nil verifiers.
	for _, scope := range []string{"screen-recording", "accessibility", "input-monitoring"} {
		for _, bin := range []string{"guiport", "um"} {
			fn, name := scopeVerifier(scope, bin)
			if fn == nil {
				t.Errorf("scope=%q bin=%q: expected verifier, got nil", scope, bin)
			}
			if name == "" {
				t.Errorf("scope=%q bin=%q: expected verifier name", scope, bin)
			}
		}
	}
	// Unknown binary returns nil — calling code must handle.
	if fn, _ := scopeVerifier("screen-recording", "made-up-tool"); fn != nil {
		t.Error("unknown binary should return nil verifier")
	}
	// Unknown scope for a known binary returns nil too (don't pretend).
	if fn, _ := scopeVerifier("camera", "guiport"); fn != nil {
		t.Error("unknown scope should return nil verifier")
	}
}
