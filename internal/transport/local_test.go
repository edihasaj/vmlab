package transport

import "testing"

func TestLocalCapabilitiesIncludeGUIOnWindows(t *testing.T) {
	got := localCapabilities("windows")
	if !got.Shell || !got.Install || !got.Screenshot || !got.GUI {
		t.Fatalf("localCapabilities(windows) = %+v, want shell, install, screenshot, and GUI", got)
	}
}

func TestLocalCapabilitiesExcludeGUIOnNonWindows(t *testing.T) {
	got := localCapabilities("darwin")
	if got.Screenshot || got.GUI {
		t.Fatalf("localCapabilities(darwin) = %+v, want screenshot and GUI disabled", got)
	}
}
