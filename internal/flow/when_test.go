package flow

import "testing"

func TestMatchWhen(t *testing.T) {
	cases := []struct {
		expr    string
		os      string
		arch    string
		want    bool
		wantErr bool
	}{
		{expr: "os=linux", os: "linux", want: true},
		{expr: "os=linux", os: "windows", want: false},
		{expr: "os!=windows", os: "linux", want: true},
		{expr: "os!=windows", os: "windows", want: false},
		{expr: "os=linux,arch=arm64", os: "linux", arch: "arm64", want: true},
		{expr: "os=linux,arch=arm64", os: "linux", arch: "amd64", want: false},
		{expr: " os = linux , arch = arm64 ", os: "linux", arch: "arm64", want: true},
		{expr: "os=mac", os: "darwin", want: true}, // mac → darwin alias
		{expr: "os=macos", os: "darwin", want: true},
		{expr: "", os: "linux", want: true},           // empty matches everything
		{expr: "os=linux,,", os: "linux", want: true}, // blank clauses tolerated
		{expr: "foo=bar", os: "linux", wantErr: true},
		{expr: "os", os: "linux", wantErr: true},
	}
	for _, c := range cases {
		got, err := matchWhen(c.expr, c.os, c.arch)
		if c.wantErr {
			if err == nil {
				t.Errorf("expr=%q expected error, got nil", c.expr)
			}
			continue
		}
		if err != nil {
			t.Errorf("expr=%q unexpected error: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("expr=%q os=%s arch=%s: got %v, want %v", c.expr, c.os, c.arch, got, c.want)
		}
	}
}

func TestPickInstall(t *testing.T) {
	m := map[string]string{
		"linux":   "apt-get install jq",
		"darwin":  "brew install jq",
		"windows": "choco install jq",
	}
	if got, ok := pickInstall(m, "linux"); !ok || got != "apt-get install jq" {
		t.Errorf("linux pick wrong: %q ok=%v", got, ok)
	}
	if got, ok := pickInstall(m, "darwin"); !ok || got != "brew install jq" {
		t.Errorf("darwin pick wrong: %q ok=%v", got, ok)
	}
	if got, ok := pickInstall(m, "windows"); !ok || got != "choco install jq" {
		t.Errorf("windows pick wrong: %q ok=%v", got, ok)
	}
	if _, ok := pickInstall(m, "ios"); ok {
		t.Errorf("ios should miss (not in map)")
	}
	// mac alias when only "mac" is set.
	m2 := map[string]string{"mac": "brew install jq"}
	if got, ok := pickInstall(m2, "darwin"); !ok || got != "brew install jq" {
		t.Errorf("mac alias should resolve when os=darwin, got %q ok=%v", got, ok)
	}
}
