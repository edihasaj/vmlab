// Package transport — adapter tests using PATH-injected fake binaries.
//
// Each test writes a tiny shell script for the underlying CLI (crabbox, adb,
// idb, xcrun, maestro, abx, guiport), prepends a temp dir to PATH, and asserts
// the adapter translates settings into the expected CLI invocation. The fake
// records its argv to a file the test inspects.
package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

// stubBinary writes a shell script at <dir>/<name> that records its argv to
// <dir>/<name>.args (one line per invocation) and exits with exitCode.
func stubBinary(t *testing.T, dir, name string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub binaries assume a POSIX shell")
	}
	argsFile := filepath.Join(dir, name+".args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
exit %d
`, argsFile, exitCode)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
	return argsFile
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func readLastArgs(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return lines[len(lines)-1]
}

func TestCrabboxRunForwardsArgs(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "crabbox", 0)
	withPath(t, dir)

	tr := NewCrabbox()
	tgt := target.Target{
		Name:      "ubuntu",
		Transport: "crabbox",
		Settings: map[string]any{
			"crabbox": map[string]any{"configPath": "/etc/crabbox.yaml"},
		},
	}
	var out, errb bytes.Buffer
	res, err := tr.Run(context.Background(), tgt, []string{"uname", "-a"}, &out, &errb)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{"--config", "/etc/crabbox.yaml", "run", "--", "uname", "-a"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestCrabboxDoctor(t *testing.T) {
	dir := t.TempDir()
	stubBinary(t, dir, "crabbox", 0)
	withPath(t, dir)

	tr := NewCrabbox()
	h := tr.Doctor(context.Background(), target.Target{Name: "x"})
	if !h.OK {
		t.Fatalf("expected OK doctor, got %s", h.Message)
	}
}

func TestCrabboxDoctorMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tr := NewCrabbox()
	h := tr.Doctor(context.Background(), target.Target{Name: "x"})
	if h.OK {
		t.Fatalf("expected unhealthy: %#v", h)
	}
	if !strings.Contains(h.Message, "PATH") {
		t.Errorf("expected PATH error, got %s", h.Message)
	}
}

func TestADBRoutesShellByDefault(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "adb", 0)
	withPath(t, dir)

	tr := NewADB()
	tgt := target.Target{Settings: map[string]any{"adb": map[string]any{"serial": "RFNX001"}}}
	_, err := tr.Run(context.Background(), tgt, []string{"whoami"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "-s RFNX001") || !strings.Contains(got, "shell whoami") {
		t.Errorf("unexpected argv: %s", got)
	}
}

func TestADBPassThroughVerbs(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "adb", 0)
	withPath(t, dir)

	tr := NewADB()
	_, err := tr.Run(context.Background(), target.Target{}, []string{"install", "app.apk"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "install app.apk") {
		t.Errorf("expected pass-through, got: %s", got)
	}
}

func TestIDBForwards(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "idb", 0)
	withPath(t, dir)

	tr := NewIDB()
	tgt := target.Target{Settings: map[string]any{"idb": map[string]any{"udid": "00008110"}}}
	_, err := tr.Run(context.Background(), tgt, []string{"list-apps"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "--udid 00008110") || !strings.Contains(got, "list-apps") {
		t.Errorf("unexpected argv: %s", got)
	}
}

func TestSimctlBootRoutesUDID(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "xcrun", 0)
	withPath(t, dir)

	tr := NewSimctl()
	tgt := target.Target{Settings: map[string]any{"simctl": map[string]any{"udid": "AAA-BBB"}}}
	_, err := tr.Run(context.Background(), tgt, []string{"boot"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "simctl boot AAA-BBB") {
		t.Errorf("unexpected argv: %s", got)
	}
}

func TestMaestroFlowPathMapsToTest(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "maestro", 0)
	withPath(t, dir)

	tr := NewMaestro()
	_, err := tr.Run(context.Background(), target.Target{}, []string{"flows/login.yaml"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "test flows/login.yaml") {
		t.Errorf("expected test wrapper, got: %s", got)
	}
}

func TestABXLiveMode(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "abx", 0)
	withPath(t, dir)

	tr := NewABX()
	tgt := target.Target{Settings: map[string]any{"abx": map[string]any{"mode": "live", "url": "https://x"}}}
	_, err := tr.Run(context.Background(), tgt, []string{"goto", "https://x"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{"live", "--url", "https://x", "goto"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestGuiportClick(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "guiport", 0)
	withPath(t, dir)

	tr := NewGuiport()
	tgt := target.Target{Settings: map[string]any{"guiport": map[string]any{"app": "Calculator", "strict": true}}}
	if err := tr.GUI(context.Background(), tgt, GUIAction{Kind: "click", Selector: "AXButton[title=9]"}); err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{"--app", "Calculator", "--strict", "click", "AXButton[title=9]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestParallelsGuestLocal(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "prlctl", 0)
	withPath(t, dir)

	tr := NewParallelsGuest()
	tgt := target.Target{
		Name:      "win11",
		Transport: "parallels-guest",
		Settings: map[string]any{
			"parallels": map[string]any{"vm": "Windows 11"},
		},
	}
	_, err := tr.Run(context.Background(), tgt, []string{"cmd.exe", "/c", "ver"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{"exec", "Windows 11", "cmd.exe", "/c", "ver"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestParallelsGuestRemoteQuoting(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewParallelsGuest()
	tgt := target.Target{
		Settings: map[string]any{
			"parallels": map[string]any{
				"host": "studio.local",
				"user": "edi",
				"vm":   "Windows 11",
			},
		},
	}
	// arg with spaces and a single quote — the layered-quote case.
	probe := []string{"powershell.exe", "-NoProfile", "-Command", "Get-Date -Format 'o'"}
	_, err := tr.Run(context.Background(), tgt, probe, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{
		"-o ConnectTimeout=8",
		"-o BatchMode=yes",
		"edi@studio.local",
		"prlctl exec",
		`'Windows 11'`,
		"powershell.exe",
		"-NoProfile",
		"-Command",
		`'Get-Date -Format '\''o'\'''`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestParallelsGuestSyncLocal(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "prlctl", 0)
	withPath(t, dir)

	tr := NewParallelsGuest()
	tgt := target.Target{
		Settings: map[string]any{"parallels": map[string]any{"vm": "Windows 11"}},
	}
	if err := tr.Sync(context.Background(), tgt, "/Users/edi/Projects/myapp"); err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{"set", "Windows 11", "--shf-host-add", "myapp", "--path", "/Users/edi/Projects/myapp"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestParallelsGuestPosixQuote(t *testing.T) {
	cases := map[string]string{
		"plain":         "plain",
		"":              "''",
		"has space":     `'has space'`,
		`with'quote`:    `'with'\''quote'`,
		`a"b`:           `'a"b'`,
		`semi;colon`:    `'semi;colon'`,
		`back\slash`:    `'back\slash'`,
	}
	for in, want := range cases {
		if got := posixQuote(in); got != want {
			t.Errorf("posixQuote(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestRunExitCode(t *testing.T) {
	dir := t.TempDir()
	stubBinary(t, dir, "crabbox", 7)
	withPath(t, dir)

	tr := NewCrabbox()
	res, err := tr.Run(context.Background(), target.Target{}, []string{"x"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("expected exit 7, got %d", res.ExitCode)
	}
}
