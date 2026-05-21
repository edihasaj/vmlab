package target

import "testing"

func TestOSKindExplicitSetting(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"linux", "linux"},
		{"windows", "windows"},
		{"darwin", "darwin"},
		{"MAC", "darwin"},
		{"macos", "darwin"},
		{"osx", "darwin"},
		{"ios", "ios"},
		{"android", "android"},
	}
	for _, c := range cases {
		tgt := Target{Settings: map[string]any{"os": c.in}}
		if got := tgt.OSKind(); got != c.want {
			t.Errorf("explicit os=%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOSKindTransportDerived(t *testing.T) {
	cases := map[string]string{
		"ssh":             "linux",
		"crabbox":         "linux",
		"local":           "linux",
		"ssh-windows":     "windows",
		"parallels-guest": "windows",
		"simctl":          "ios",
		"idb":             "ios",
		"adb":             "android",
		"guiport":         "darwin",
		"abx":             "darwin",
		"maestro":         "unknown", // no default mapping yet
	}
	for transport, want := range cases {
		tgt := Target{Transport: transport}
		if got := tgt.OSKind(); got != want {
			t.Errorf("transport=%q: got %q, want %q", transport, got, want)
		}
	}
}

func TestOSKindExplicitOverridesTransport(t *testing.T) {
	// ssh-windows transport but the user explicitly says it's WSL/Linux.
	tgt := Target{
		Transport: "ssh-windows",
		Settings:  map[string]any{"os": "linux"},
	}
	if got := tgt.OSKind(); got != "linux" {
		t.Errorf("explicit must override transport default; got %q", got)
	}
}
