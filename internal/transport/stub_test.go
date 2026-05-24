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
	"time"

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

func TestADBSyncPushesToDevice(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "adb", 0)
	withPath(t, dir)

	tr := NewADB()
	tgt := target.Target{Settings: map[string]any{"adb": map[string]any{"serial": "RFNX001", "dest": "/sdcard/Download/app"}}}
	if err := tr.Sync(context.Background(), tgt, "./build/app.apk"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got := readLastArgs(t, args)
	for _, want := range []string{"-s RFNX001", "push", "./build/app.apk", "/sdcard/Download/app"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func TestADBSyncDefaultDest(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "adb", 0)
	withPath(t, dir)

	tr := NewADB()
	if err := tr.Sync(context.Background(), target.Target{}, "./src"); err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "push ./src /sdcard/vmlab") {
		t.Errorf("expected default /sdcard/vmlab dest, got: %s", got)
	}
}

func TestIDBSyncSurfacesLimitation(t *testing.T) {
	tr := NewIDB()
	err := tr.Sync(context.Background(), target.Target{}, "./src")
	if err == nil {
		t.Fatal("expected error explaining bundle-scoped limitation")
	}
	if !strings.Contains(err.Error(), "bundle") {
		t.Errorf("expected bundle-related error, got: %v", err)
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
	// Live mode wraps gui: actions as `abx live <verb>` — drives the
	// user's real Chrome via CDP. Run() shells out locally so it doesn't
	// exercise the live wrap; we test via GUI() instead.
	dir := t.TempDir()
	args := stubBinary(t, dir, "abx", 0)
	withPath(t, dir)

	tr := NewABX()
	tgt := target.Target{Settings: map[string]any{"abx": map[string]any{"mode": "live"}}}
	if err := tr.GUI(context.Background(), tgt, GUIAction{Kind: "open-url", Path: "https://x"}); err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "live goto https://x") {
		t.Errorf("expected 'live goto https://x' in argv: %s", got)
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
	// guiport CLI takes --app and --strict as per-subcommand flags after the verb.
	if !strings.Contains(got, "click AXButton[title=9] --app Calculator --strict") {
		t.Errorf("unexpected argv: %s", got)
	}
}

func TestGuiportExpandedKinds(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "guiport", 0)
	withPath(t, dir)

	tr := NewGuiport()
	tgt := target.Target{Settings: map[string]any{"guiport": map[string]any{"app": "TextEdit"}}}
	ctx := context.Background()

	cases := []struct {
		name   string
		action GUIAction
		want   []string
	}{
		{
			name:   "click-text",
			action: GUIAction{Kind: "click-text", Text: "Save"},
			want:   []string{"click-text Save --app TextEdit"},
		},
		{
			name:   "click-at",
			action: GUIAction{Kind: "click-at", Extra: map[string]any{"x": 120, "y": 240}},
			want:   []string{"click-at 120 240"},
		},
		{
			name:   "type-no-app-flag",
			action: GUIAction{Kind: "type", Text: "hello"},
			want:   []string{"type hello"},
		},
		{
			name:   "hotkey-via-text",
			action: GUIAction{Kind: "hotkey", Text: "cmd+space"},
			want:   []string{"hotkey cmd+space"},
		},
		{
			name:   "hotkey-via-selector-fallback",
			action: GUIAction{Kind: "hotkey", Selector: "cmd+shift+4"},
			want:   []string{"hotkey cmd+shift+4"},
		},
		{
			name:   "observe",
			action: GUIAction{Kind: "observe"},
			want:   []string{"observe --app TextEdit"},
		},
		{
			name:   "tree",
			action: GUIAction{Kind: "tree"},
			want:   []string{"tree --app TextEdit"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tr.GUI(ctx, tgt, tc.action); err != nil {
				t.Fatalf("GUI %s: %v", tc.action.Kind, err)
			}
			got := readLastArgs(t, args)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in argv: %s", want, got)
				}
			}
		})
	}
}

func TestGuiportWaitIsLocal(t *testing.T) {
	// `wait` must not call out to guiport — it's a transport-side sleep.
	dir := t.TempDir()
	args := stubBinary(t, dir, "guiport", 0)
	withPath(t, dir)

	tr := NewGuiport()
	tgt := target.Target{}
	start := time.Now()
	if err := tr.GUI(context.Background(), tgt, GUIAction{Kind: "wait", Extra: map[string]any{"milliseconds": 120}}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("wait returned too fast (%s); expected >=100ms", elapsed)
	}
	if _, err := os.Stat(args); err == nil {
		t.Fatal("wait must not invoke the guiport binary")
	}
}

func TestABXGUIKinds(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "abx", 0)
	withPath(t, dir)

	tr := NewABX()
	tgt := target.Target{}
	ctx := context.Background()

	cases := []struct {
		name   string
		action GUIAction
		want   []string
	}{
		{
			name:   "screenshot",
			action: GUIAction{Kind: "screenshot", Path: "/tmp/web.png"},
			want:   []string{"screenshot /tmp/web.png"},
		},
		{
			name:   "click",
			action: GUIAction{Kind: "click", Selector: "button[name='Save']"},
			want:   []string{"click button[name='Save']"},
		},
		{
			name:   "click-text",
			action: GUIAction{Kind: "click-text", Text: "Sign in"},
			want:   []string{"click text=Sign in"},
		},
		{
			name:   "type",
			action: GUIAction{Kind: "type", Text: "vmlab@example.com"},
			want:   []string{"type vmlab@example.com"},
		},
		{
			name:   "hotkey",
			action: GUIAction{Kind: "hotkey", Text: "Enter"},
			want:   []string{"press Enter"},
		},
		{
			name:   "wait-selector",
			action: GUIAction{Kind: "wait", Selector: ".loaded"},
			want:   []string{"wait .loaded"},
		},
		{
			name:   "observe",
			action: GUIAction{Kind: "observe"},
			want:   []string{"accessibility"},
		},
		{
			name:   "open-url",
			action: GUIAction{Kind: "open-url", Path: "https://recallmemory.dev"},
			want:   []string{"goto https://recallmemory.dev"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tr.GUI(ctx, tgt, tc.action); err != nil {
				t.Fatalf("GUI %s: %v", tc.action.Kind, err)
			}
			got := readLastArgs(t, args)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in argv: %s", want, got)
				}
			}
		})
	}
}

func TestABXWaitIsLocal(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "abx", 0)
	withPath(t, dir)

	tr := NewABX()
	start := time.Now()
	if err := tr.GUI(context.Background(), target.Target{}, GUIAction{Kind: "wait", Extra: map[string]any{"milliseconds": 100}}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("wait returned too fast (%s)", elapsed)
	}
	if _, err := os.Stat(args); err == nil {
		t.Fatal("wait must not invoke the abx binary when no selector given")
	}
}

func TestUndermouseGUI(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "um", 0)
	withPath(t, dir)

	tr := NewUnderMouse()
	tgt := target.Target{}
	ctx := context.Background()

	cases := []struct {
		name   string
		action GUIAction
		want   []string
	}{
		{
			name:   "screenshot",
			action: GUIAction{Kind: "screenshot", Path: "/tmp/shot.png"},
			want:   []string{"screenshot /tmp/shot.png"},
		},
		{
			name:   "ask-text",
			action: GUIAction{Kind: "ask", Text: "what time is it?"},
			want:   []string{"ask what time is it?"},
		},
		{
			name:   "context",
			action: GUIAction{Kind: "context"},
			want:   []string{"context"},
		},
		{
			name:   "click-via-act-plan",
			action: GUIAction{Kind: "click", Selector: "AXButton[title=Save]"},
			want:   []string{"act --plan -"},
		},
		{
			name:   "click-text-via-act-plan",
			action: GUIAction{Kind: "click-text", Text: "Save"},
			want:   []string{"act --plan -"},
		},
		{
			name:   "hotkey-via-act-plan",
			action: GUIAction{Kind: "hotkey", Text: "cmd+space"},
			want:   []string{"act --plan -"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tr.GUI(ctx, tgt, tc.action); err != nil {
				t.Fatalf("GUI %s: %v", tc.action.Kind, err)
			}
			got := readLastArgs(t, args)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in argv: %s", want, got)
				}
			}
		})
	}
}

func TestUndermouseWaitIsLocal(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "um", 0)
	withPath(t, dir)

	tr := NewUnderMouse()
	start := time.Now()
	if err := tr.GUI(context.Background(), target.Target{}, GUIAction{Kind: "wait", Extra: map[string]any{"milliseconds": 100}}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("wait returned too fast (%s); expected >=90ms", elapsed)
	}
	if _, err := os.Stat(args); err == nil {
		t.Fatal("wait must not invoke the um binary")
	}
}

func TestUndermouseDoctorMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	tr := NewUnderMouse()
	h := tr.Doctor(context.Background(), target.Target{})
	if h.OK {
		t.Fatalf("expected unhealthy when um not on PATH: %#v", h)
	}
	if !strings.Contains(h.Message, "PATH") {
		t.Errorf("expected PATH error, got %s", h.Message)
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
		"plain":      "plain",
		"":           "''",
		"has space":  `'has space'`,
		`with'quote`: `'with'\''quote'`,
		`a"b`:        `'a"b'`,
		`semi;colon`: `'semi;colon'`,
		`back\slash`: `'back\slash'`,
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
