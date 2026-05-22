package transport

import (
	"path/filepath"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

func TestShareNameFromSrcResolvesDotToCwdBasename(t *testing.T) {
	got := shareNameFromSrc(".")
	abs, _ := filepath.Abs(".")
	want := filepath.Base(abs)
	if got != want {
		t.Fatalf("share name for `.` = %q, want %q", got, want)
	}
}

func TestShareNameFromSrcKeepsTrailingComponent(t *testing.T) {
	cases := map[string]string{
		"/Users/edi/Projects/recall":     "recall",
		"C:\\src\\repo":                  "repo",
		"./flows":                        "flows",
		"":                               "vmlab-sync",
	}
	for in, want := range cases {
		if got := shareNameFromSrc(in); got != want {
			t.Errorf("shareNameFromSrc(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldStageLocallyMissingPathSkipsStaging(t *testing.T) {
	// Regression: if src doesn't exist on the laptop, fall through to the
	// classic "host can read it directly" path instead of failing the rsync.
	if shouldStageLocally("/definitely/not/here/abc123") {
		t.Fatal("expected false for non-existent path")
	}
}

func TestParallelsGuestSyncRequiresVM(t *testing.T) {
	tr := &parallelsGuestTransport{}
	err := tr.Sync(t.Context(), target.Target{Name: "x", Transport: "parallels-guest"}, "/tmp")
	if err == nil || err.Error() != "parallels-guest: parallels.vm is required" {
		t.Fatalf("expected parallels.vm error, got %v", err)
	}
}
