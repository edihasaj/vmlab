package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/edihasaj/vmlab/internal/target"
)

func TestSSHWindowsPwshDefaultUsesEncodedCommand(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewSSHWindows()
	tgt := target.Target{
		Name:      "win11",
		Transport: "ssh-windows",
		Settings: map[string]any{
			"ssh": map[string]any{
				"host": "win11.lan",
				"user": "edi",
			},
		},
	}
	probe := []string{"powershell.exe", "-Command", "Get-Date -Format 'o'"}
	_, err := tr.Run(context.Background(), tgt, probe, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)

	if !strings.Contains(got, "edi@win11.lan") {
		t.Errorf("missing user@host: %s", got)
	}
	if !strings.Contains(got, "pwsh -NoProfile -NonInteractive -EncodedCommand") {
		t.Errorf("expected pwsh -EncodedCommand path: %s", got)
	}

	enc := extractEncodedCommand(t, got)
	decoded := decodePowerShell(t, enc)
	// The original args must survive the round-trip with PowerShell single
	// quotes; the embedded single quote in 'o' is doubled to ''o''.
	wantFragments := []string{
		"& 'powershell.exe' '-Command' 'Get-Date -Format ''o'''",
	}
	for _, w := range wantFragments {
		if !strings.Contains(decoded, w) {
			t.Errorf("decoded ps did not contain %q\ngot: %s", w, decoded)
		}
	}
}

func TestSSHWindowsCmdShellQuotes(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewSSHWindows()
	tgt := target.Target{
		Settings: map[string]any{
			"ssh": map[string]any{
				"host":  "win.lan",
				"shell": "cmd",
			},
		},
	}
	_, err := tr.Run(context.Background(), tgt, []string{"echo", `hello "world"`}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	// the doubly-quoted form: echo "hello \"world\""
	if !strings.Contains(got, `echo "hello \"world\""`) {
		t.Errorf("expected cmd-style escaping, got: %s", got)
	}
}

func TestSSHWindowsNoneShellPassthrough(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewSSHWindows()
	tgt := target.Target{
		Settings: map[string]any{
			"ssh": map[string]any{
				"host":  "win.lan",
				"shell": "none",
			},
		},
	}
	_, err := tr.Run(context.Background(), tgt, []string{"already-quoted-by-caller"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got := readLastArgs(t, args)
	if !strings.HasSuffix(strings.TrimSpace(got), "already-quoted-by-caller") {
		t.Errorf("expected pass-through tail, got: %s", got)
	}
}

func TestSSHWindowsDoctorMissingHost(t *testing.T) {
	dir := t.TempDir()
	stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewSSHWindows()
	h := tr.Doctor(context.Background(), target.Target{})
	if h.OK {
		t.Fatalf("expected unhealthy, got %+v", h)
	}
	if !strings.Contains(h.Message, "ssh.host") {
		t.Errorf("expected ssh.host error, got: %s", h.Message)
	}
}

func TestSSHWindowsDoctorPwshProbe(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewSSHWindows()
	tgt := target.Target{
		Settings: map[string]any{
			"ssh": map[string]any{"host": "win.lan"},
		},
	}
	h := tr.Doctor(context.Background(), tgt)
	if !h.OK {
		t.Fatalf("expected OK doctor, got %+v", h)
	}
	got := readLastArgs(t, args)
	if !strings.Contains(got, "-EncodedCommand") {
		t.Errorf("expected encoded pwsh probe, got: %s", got)
	}
	enc := extractEncodedCommand(t, got)
	decoded := decodePowerShell(t, enc)
	// The probe must be an invocable command (hostname.exe), not a bare
	// expression — vmlab wraps argv as `& 'arg0' …`, so an expression like
	// $PSVersionTable would become `& '$PSVersionTable…'` and fail.
	if !strings.Contains(decoded, "hostname") {
		t.Errorf("expected hostname probe in decoded ps: %s", decoded)
	}
}

func TestParseElevatedOutbox(t *testing.T) {
	raw := []byte(`{"exitCode":7,"stdout":"hello\n","stderr":"warn\n"}`)
	var so, se bytes.Buffer
	res, err := parseElevatedOutbox(raw, &so, &se)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit=%d", res.ExitCode)
	}
	if so.String() != "hello\n" || se.String() != "warn\n" {
		t.Errorf("stdout/stderr mismatch: %q / %q", so.String(), se.String())
	}
}

func TestSSHWindowsElevatedFlagsRouteThroughTask(t *testing.T) {
	dir := t.TempDir()
	args := stubBinary(t, dir, "ssh", 0)
	withPath(t, dir)

	tr := NewSSHWindows()
	tgt := target.Target{
		Settings: map[string]any{
			"ssh": map[string]any{
				"host":     "win.lan",
				"elevated": true,
			},
		},
	}
	// We don't need the result to make sense — only that the stage script
	// referenced schtasks /run and the configured task name. The payload
	// is wrapped in -EncodedCommand (UTF-16LE base64); decode first.
	_, _ = tr.Run(context.Background(), tgt, []string{"powershell.exe", "-Command", "ver"}, io.Discard, io.Discard)
	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	enc := extractEncodedCommand(t, string(raw))
	decoded := decodePowerShell(t, enc)
	if !strings.Contains(decoded, "schtasks /run /tn") || !strings.Contains(decoded, "vmlab-elevated") {
		t.Errorf("expected schtasks routing with default task name in decoded payload:\n%s", decoded)
	}
	if !strings.Contains(decoded, `C:\ProgramData\vmlab\inbox\next.ps1`) {
		t.Errorf("expected inbox staging in decoded payload:\n%s", decoded)
	}
}

func TestSSHWindowsApproveIteratesLabels(t *testing.T) {
	dir := t.TempDir()
	// Stub ssh: succeed only when the encoded PowerShell payload contains
	// our chosen target label "Allow". Anything else exits 1 (no match).
	// This simulates UIA returning "no element with Name containing X".
	// Stub ssh: fail the first invocation (OK), succeed on the second
	// (Allow). The exact label can't be cheaply parsed from the
	// PowerShell EncodedCommand here, so use the call-count proxy.
	script := `#!/bin/sh
count_file="` + dir + `/count"
n=$(cat "$count_file" 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > "$count_file"
if [ "$n" -ge 2 ]; then
  echo "clicked" >> "` + dir + `/clicked"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	withPath(t, dir)

	tr := NewSSHWindows()
	tgt := target.Target{
		Settings: map[string]any{
			"ssh": map[string]any{"host": "win.lan"},
		},
	}
	err := tr.GUI(context.Background(), tgt, GUIAction{
		Kind:  "approve",
		Extra: map[string]any{"allow": []any{"OK", "Allow"}, "timeout": "3s"},
	}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "clicked")); err != nil {
		t.Fatalf("expected Allow click to register, got: %v", err)
	}
}

func TestSSHWindowsCmdQuoteCases(t *testing.T) {
	cases := map[string]string{
		"plain":        "plain",
		"":             `""`,
		"has space":    `"has space"`,
		`has "quotes"`: `"has \"quotes\""`,
		`mid\path`:     `mid\path`, // no special chars -> verbatim
		`a&b`:          `"a&b"`,
		`a|b`:          `"a|b"`,
		`trail back\\`: `"trail back\\\\"`, // trailing \\ doubled inside quotes
		`pre\"quote`:   `"pre\\\"quote"`,   // \ before " also doubled
	}
	for in, want := range cases {
		if got := cmdQuote(in); got != want {
			t.Errorf("cmdQuote(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestPSSingleQuoteEscapesQuotes(t *testing.T) {
	cases := map[string]string{
		"x":            "'x'",
		"":             "''",
		"with'quote":   "'with''quote'",
		"two''already": "'two''''already'",
	}
	for in, want := range cases {
		if got := psSingleQuote(in); got != want {
			t.Errorf("psSingleQuote(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestEncodePowerShellRoundTrip(t *testing.T) {
	in := "Write-Host 'hello, world ⚡'"
	enc := encodePowerShell(in)
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("UTF-16LE bytes must be even length, got %d", len(raw))
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	got := string(utf16.Decode(u16))
	if got != in {
		t.Errorf("round-trip failed:\n in=%q\nout=%q", in, got)
	}
}

// extractEncodedCommand pulls the base64 token after `-EncodedCommand` from
// the recorded argv. The arg is the last token after the flag.
func extractEncodedCommand(t *testing.T, argv string) string {
	t.Helper()
	idx := strings.Index(argv, "-EncodedCommand ")
	if idx < 0 {
		t.Fatalf("no -EncodedCommand in argv: %s", argv)
	}
	rest := argv[idx+len("-EncodedCommand "):]
	// Take the first whitespace-bounded token.
	if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func decodePowerShell(t *testing.T, enc string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("UTF-16LE bytes must be even, got %d", len(raw))
	}
	u16 := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return string(utf16.Decode(u16))
}
