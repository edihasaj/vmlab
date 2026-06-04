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
// the argv in a PowerShell -EncodedCommand whose decoded payload invokes the
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

	// The call operator invokes arg0 with the rest as literal positional args.
	if !strings.HasPrefix(payload, "& ") {
		t.Errorf("payload should start with call operator, got %q", payload)
	}
	// Exit-code propagation so flows/matrix observe true pass/fail.
	if !strings.HasSuffix(payload, "; exit $LASTEXITCODE") {
		t.Errorf("payload should propagate exit code, got %q", payload)
	}
	// The pipe and backslash path must survive verbatim inside the single-quoted
	// argument — this is exactly what the raw prlctl path destroyed.
	if !strings.Contains(payload, "Get-Content C:\\dayshape\\build.log | Select-String ''error''") {
		t.Errorf("argument not preserved verbatim in payload: %q", payload)
	}
}
