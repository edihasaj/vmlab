package tart

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

func stubTart(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	argsFile := filepath.Join(dir, "tart.args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
%s
`, argsFile, body)
	path := filepath.Join(dir, "tart")
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
	return provider.Instance{
		Name:     name,
		Provider: "tart",
		Settings: map[string]any{"tart": extra},
	}
}

func TestStatusRunning(t *testing.T) {
	dir := t.TempDir()
	stubTart(t, dir, `cat <<JSON
[{"Name":"smoke","State":"running","Running":true,"Source":"local"}]
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

func TestStatusSuspendedMapsToStopped(t *testing.T) {
	dir := t.TempDir()
	stubTart(t, dir, `cat <<JSON
[{"Name":"smoke","State":"suspended","Running":false,"Source":"local"}]
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
	stubTart(t, dir, `echo "[]"; exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("ghost", nil))
	if err != nil || st != provider.StateNotFound {
		t.Fatalf("status=%v err=%v", st, err)
	}
}

func TestDownSuspendCallsSuspend(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubTart(t, dir, `case "$*" in
  *"list"*) cat <<JSON
[{"Name":"smoke","State":"running","Running":true,"Source":"local"}]
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeSuspend); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "suspend smoke") {
		t.Fatalf("expected suspend; got:\n%s", got)
	}
}

func TestDownDestroyCallsStopThenDelete(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubTart(t, dir, `case "$*" in
  *"list"*) cat <<JSON
[{"Name":"smoke","State":"running","Running":true,"Source":"local"}]
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeDestroy); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "stop smoke") || !strings.Contains(string(got), "delete smoke") {
		t.Fatalf("expected stop+delete; got:\n%s", got)
	}
}

func TestDoctorOK(t *testing.T) {
	dir := t.TempDir()
	stubTart(t, dir, `echo "[]"; exit 0`)
	withPath(t, dir)

	h := New().Doctor(context.Background(), instance("smoke", nil))
	if !h.OK {
		t.Fatalf("doctor=%+v", h)
	}
}

func TestUpRequiresSourceWhenNotFound(t *testing.T) {
	dir := t.TempDir()
	stubTart(t, dir, `echo "[]"; exit 0`)
	withPath(t, dir)

	_, _, err := New().Up(context.Background(), instance("smoke", nil))
	if err == nil || !strings.Contains(err.Error(), "tart.source") {
		t.Fatalf("expected tart.source error, got %v", err)
	}
}
