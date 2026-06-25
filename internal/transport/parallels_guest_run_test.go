package transport

import (
	"strings"
	"testing"
)

// decodePowerShell (base64 → UTF-16LE → string) is defined in sshwindows_test.go.

// encodedPayload extracts the token after -EncodedCommand from a recorded
// argv line and decodes it back to the PowerShell payload, so tests can assert
// the wrapped Windows-guest command round-trips verbatim.
func encodedPayload(t *testing.T, argv string) string {
	t.Helper()
	fields := strings.Fields(argv)
	for i, f := range fields {
		if f == "-EncodedCommand" && i+1 < len(fields) {
			return decodePowerShell(t, fields[i+1])
		}
	}
	t.Fatalf("no -EncodedCommand token in argv: %s", argv)
	return ""
}

// TestWinGuestArgv is the regression guard for the bug where Windows-guest
// commands run via parallels-guest were shredded by the ssh→prlctl→cmd.exe
// layers (pipes swallowed, `C:\x` collapsed to `C:x`). winGuestArgv must wrap
// the argv in a PowerShell -EncodedCommand whose decoded payload launches the
// original argv verbatim — pipes, backslashes and quotes intact.
func TestWinGuestArgv(t *testing.T) {
	cmd := []string{"powershell", "-Command", "Get-Content C:\\dayshape\\build.log | Select-String 'error'"}

	argv, err := winGuestArgv(cmd)
	if err != nil {
		t.Fatalf("winGuestArgv: %v", err)
	}

	if len(argv) != 5 ||
		argv[0] != "powershell.exe" ||
		argv[1] != "-NoProfile" ||
		argv[2] != "-NonInteractive" ||
		argv[3] != "-EncodedCommand" {
		t.Fatalf("unexpected wrapper argv: %#v", argv)
	}

	payload := decodePowerShell(t, argv[4])

	// Launch via ProcessStartInfo with our own quoting — the path that
	// sidesteps PowerShell 5.1's broken native-argument quoting.
	if !strings.Contains(payload, "System.Diagnostics.ProcessStartInfo") ||
		!strings.Contains(payload, "$si.UseShellExecute=$false") {
		t.Errorf("payload should launch via ProcessStartInfo, got %q", payload)
	}
	// Exit-code propagation so flows/matrix observe true pass/fail.
	if !strings.HasSuffix(payload, "exit $p.ExitCode") {
		t.Errorf("payload should propagate exit code, got %q", payload)
	}
	// The pipe and backslash path must survive verbatim inside the quoted
	// argument line — this is exactly what the raw prlctl path destroyed. The
	// arg contains a space + pipe, so cmdQuote wraps it in double quotes; the
	// embedded single quotes are doubled by psSingleQuote.
	if !strings.Contains(payload, `"Get-Content C:\dayshape\build.log | Select-String ''error''"`) {
		t.Errorf("argument not preserved verbatim in payload: %q", payload)
	}
}

// TestWinGuestArgvNativeSpacedArg is the regression guard for the specific
// failure that motivated the ProcessStartInfo rewrite: a single argument that
// contains spaces (e.g. a SQL query passed to sqlcmd via -Q) must reach the
// child as ONE argument. With the old `& 'sqlcmd' '-Q' 'SELECT a FROM b'`
// pattern, Windows PowerShell 5.1 dropped the quoting and sqlcmd saw `SELECT`,
// `a`, `b` as separate tokens. The fixed payload must carry the query wrapped
// in double quotes so CommandLineToArgvW keeps it intact.
func TestWinGuestArgvNativeSpacedArg(t *testing.T) {
	cmd := []string{"sqlcmd", "-S", "localhost", "-Q", "SELECT a FROM b WHERE x='y'"}

	argv, err := winGuestArgv(cmd)
	if err != nil {
		t.Fatalf("winGuestArgv: %v", err)
	}
	payload := decodePowerShell(t, argv[len(argv)-1])

	// The spaced query must be one double-quoted token in the command line
	// (single quotes inside it doubled by psSingleQuote for the PS literal).
	if !strings.Contains(payload, `-Q "SELECT a FROM b WHERE x=''y''"`) {
		t.Errorf("spaced -Q argument was not kept as a single quoted token: %q", payload)
	}
	// Flag args with no spaces stay bare (no needless quoting).
	if !strings.Contains(payload, "-S localhost") {
		t.Errorf("simple args should pass through unquoted: %q", payload)
	}
}
