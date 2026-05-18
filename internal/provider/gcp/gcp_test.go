package gcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
)

func stubGcloud(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	argsFile := filepath.Join(dir, "gcloud.args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
%s
`, argsFile, body)
	path := filepath.Join(dir, "gcloud")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return argsFile
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func instance(name string, extra map[string]any) provider.Instance {
	if extra == nil {
		extra = map[string]any{}
	}
	if _, ok := extra["project"]; !ok {
		extra["project"] = "vmlab-dev"
	}
	if _, ok := extra["zone"]; !ok {
		extra["zone"] = "us-central1-a"
	}
	return provider.Instance{
		Name:     name,
		Provider: "gcp",
		Settings: map[string]any{"gcp": extra},
	}
}

func TestStatusRunning(t *testing.T) {
	dir := t.TempDir()
	stubGcloud(t, dir, `cat <<JSON
{"status":"RUNNING"}
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st != provider.StateRunning {
		t.Errorf("state=%v", st)
	}
}

func TestStatusTerminatedMapsToStopped(t *testing.T) {
	dir := t.TempDir()
	stubGcloud(t, dir, `cat <<JSON
{"status":"TERMINATED"}
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("smoke", nil))
	if err != nil || st != provider.StateStopped {
		t.Fatalf("status=%v err=%v", st, err)
	}
}

func TestStatusNotFound(t *testing.T) {
	dir := t.TempDir()
	stubGcloud(t, dir, `echo "ERROR: resource 'ghost' was not found"; exit 1`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("ghost", nil))
	if err != nil || st != provider.StateNotFound {
		t.Fatalf("status=%v err=%v", st, err)
	}
}

func TestCreateUsesImageFamilyByDefault(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubGcloud(t, dir, `exit 0`)
	withPath(t, dir)

	if err := New().create(context.Background(), instance("smoke", nil)); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"compute instances create smoke", "--zone us-central1-a",
		"--machine-type e2-micro", "--image-family debian-12",
		"--image-project debian-cloud", "--labels vmlab=smoke"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in args:\n%s", want, got)
		}
	}
}

func TestDownDestroyUsesQuiet(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubGcloud(t, dir, `case "$*" in
  *describe*) cat <<JSON
{"status":"RUNNING"}
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeDestroy); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "instances delete smoke") || !strings.Contains(string(got), "--quiet") {
		t.Fatalf("expected instances delete ... --quiet:\n%s", got)
	}
}

func TestDownSuspendCallsStop(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubGcloud(t, dir, `case "$*" in
  *describe*) cat <<JSON
{"status":"RUNNING"}
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeSuspend); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "instances stop smoke") {
		t.Fatalf("expected instances stop:\n%s", got)
	}
}

func TestDoctorReportsActiveAccount(t *testing.T) {
	dir := t.TempDir()
	stubGcloud(t, dir, `cat <<JSON
[{"account":"edi@example.com","status":"ACTIVE"}]
JSON
exit 0`)
	withPath(t, dir)

	h := New().Doctor(context.Background(), instance("smoke", nil))
	if !h.OK || !strings.Contains(h.Message, "edi@example.com") {
		t.Fatalf("doctor=%+v", h)
	}
}

func TestDoctorNoProjectFails(t *testing.T) {
	dir := t.TempDir()
	stubGcloud(t, dir, `exit 0`)
	withPath(t, dir)

	i := provider.Instance{Name: "smoke", Provider: "gcp", Settings: map[string]any{"gcp": map[string]any{}}}
	if h := New().Doctor(context.Background(), i); h.OK {
		t.Fatalf("expected failure for missing project: %+v", h)
	}
}

func TestProjectFlagPrepended(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubGcloud(t, dir, `exit 0`)
	withPath(t, dir)

	_, _ = New().run(context.Background(), instance("smoke", nil), "compute", "instances", "list")
	got, _ := os.ReadFile(argsFile)
	line := strings.TrimSpace(string(got))
	if !strings.HasPrefix(line, "--project vmlab-dev") {
		t.Fatalf("project flag not prepended: %q", line)
	}
}
