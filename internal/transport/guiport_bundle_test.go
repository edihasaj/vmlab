package transport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

func TestGuiportAppBundleOverride(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "guiport.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	tgt := target.Target{
		Name:      "ui",
		Transport: "guiport",
		Settings:  map[string]any{"guiport": map[string]any{"appBundle": app}},
	}
	if got := guiportAppBundle(tgt); got != app {
		t.Fatalf("override bundle: got %q want %q", got, app)
	}
}

func TestGuiportAppBundleEnvOff(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "guiport.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	// Even with a valid override present, VMLAB_GUIPORT_APP=off disables routing.
	t.Setenv("VMLAB_GUIPORT_APP", "off")
	tgt := target.Target{
		Name:     "ui",
		Settings: map[string]any{"guiport": map[string]any{"appBundle": app}},
	}
	if got := guiportAppBundle(tgt); got != "" {
		t.Fatalf("env off should disable routing, got %q", got)
	}
}

func TestGuiportAppBundleEnvPath(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "guiport.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VMLAB_GUIPORT_APP", app)
	if got := guiportAppBundle(target.Target{Name: "ui"}); got != app {
		t.Fatalf("env path should win, got %q want %q", got, app)
	}
}

func TestGuiportAppBundleMissingOverrideIgnored(t *testing.T) {
	t.Setenv("VMLAB_GUIPORT_APP", "")
	// A non-existent override must not be returned; discovery falls through to
	// the well-known locations (absent in the test env -> "").
	tgt := target.Target{
		Name:      "ui",
		Transport: "guiport",
		Settings:  map[string]any{"guiport": map[string]any{"appBundle": "/no/such/guiport.app"}},
	}
	got := guiportAppBundle(tgt)
	if got == "/no/such/guiport.app" {
		t.Fatalf("missing override should be ignored, got %q", got)
	}
}
