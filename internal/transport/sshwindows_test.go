package transport

import (
	"context"
	"encoding/base64"
	"io"
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
	if !strings.Contains(decoded, "PSVersionTable") {
		t.Errorf("expected PSVersionTable probe in decoded ps: %s", decoded)
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
