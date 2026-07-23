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
	cmd := []string{"powershell", "-Command", "Get-Content C:\\proj\\build.log | Select-String 'error'"}

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
	if !strings.Contains(payload, `"Get-Content C:\proj\build.log | Select-String ''error''"`) {
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

// TestWinGuestArgvSilencesProgress guards the fix for the CLIXML noise that
// `prlctl exec` surfaced on the caller's console: PowerShell's progress stream
// ("Preparing modules for first use…") was serialized as <Objs> ahead of the
// real stdout. The payload must set $ProgressPreference='SilentlyContinue'.
func TestWinGuestArgvSilencesProgress(t *testing.T) {
	argv, err := winGuestArgv([]string{"hostname"})
	if err != nil {
		t.Fatalf("winGuestArgv: %v", err)
	}
	payload := decodePowerShell(t, argv[len(argv)-1])
	if !strings.Contains(payload, "$ProgressPreference='SilentlyContinue'") {
		t.Errorf("payload should silence the progress stream, got %q", payload)
	}
}

// TestWinNativeScriptCmdExeSinglePayloadVerbatim guards the fix for
// `vmlab run <win> -- cmd /c "a & b"` (and any single command string wrapped by
// WrapShell as `cmd.exe /c <line>`): the payload after /c must reach cmd.exe
// verbatim, NOT cmdQuote'd. The old code emitted `/c "cmd /c \"a & b\""`, whose
// `\"` cmd.exe treats literally, producing `'\"a & b\"' is not recognized`.
func TestWinNativeScriptCmdExeSinglePayloadVerbatim(t *testing.T) {
	// The shape WrapShell emits for a single command string on a Windows target.
	script := winNativeScript([]string{"cmd.exe", "/c", `cmd /c "echo AA & echo BB"`})

	if !strings.Contains(script, `$a='/c cmd /c "echo AA & echo BB"'`) {
		t.Errorf("cmd.exe payload should be passed verbatim after /c, got:\n%s", script)
	}
	// The regression signature: cmd.exe must never receive CRT-style \" escaping.
	if strings.Contains(script, `\"`) {
		t.Errorf("cmd.exe payload must not be CRT-quoted (no \\\"), got:\n%s", script)
	}
}

// TestWinNativeScriptCmdExeArgvCollapseVerbatim covers the working multi-token
// form `-- cmd /c "echo AA & echo BB"` (argv collapses to three tokens): the
// single payload after /c is still passed verbatim.
func TestWinNativeScriptCmdExeArgvCollapseVerbatim(t *testing.T) {
	script := winNativeScript([]string{"cmd", "/c", "echo AA & echo BB"})
	if !strings.Contains(script, `$a='/c echo AA & echo BB'`) {
		t.Errorf("cmd.exe single payload should be verbatim, got:\n%s", script)
	}
}

// TestWinNativeScriptCmdExeMultiArgStillQuoted ensures the verbatim path is
// scoped to a single payload: a cmd.exe invocation with several discrete args
// after /c still gets CRT-correct cmdQuote so a spaced program path stays one
// token.
func TestWinNativeScriptCmdExeMultiArgStillQuoted(t *testing.T) {
	script := winNativeScript([]string{"cmd", "/c", "my prog.exe", "x"})
	if !strings.Contains(script, `$a='/c "my prog.exe" x'`) {
		t.Errorf("multi-arg cmd.exe should keep cmdQuote, got:\n%s", script)
	}
}

// TestWinNativeScriptNonCmdUnaffected guards that the verbatim carve-out only
// applies to cmd.exe: other programs keep the cmdQuote path unchanged.
func TestWinNativeScriptNonCmdUnaffected(t *testing.T) {
	script := winNativeScript([]string{"sqlcmd", "-Q", "SELECT a FROM b"})
	if !strings.Contains(script, `$a='-Q "SELECT a FROM b"'`) {
		t.Errorf("non-cmd program should keep cmdQuote, got:\n%s", script)
	}
}

// TestWinuiScriptClickAt guards the click-at kind added for WebView2 surfaces
// (Teams/Electron/browser) where UIA InvokePattern is unreliable — it must
// emit a raw SetCursorPos + mouse_event at the requested coords.
func TestWinuiScriptClickAt(t *testing.T) {
	script, err := winuiScript(GUIAction{Kind: "click-at", Extra: map[string]any{"x": 640, "y": 512}})
	if err != nil {
		t.Fatalf("winuiScript click-at: %v", err)
	}
	for _, want := range []string{"SetCursorPos(640, 512)", "mouse_event(0x2", "mouse_event(0x4"} {
		if !strings.Contains(script, want) {
			t.Errorf("click-at script missing %q\n%s", want, script)
		}
	}
}

// TestWinuiScriptTreeFlatDump guards that tree roots at the foreground window
// and enumerates named descendants (a depth-limited walker misses deep
// WebView2 content).
func TestWinuiScriptTreeFlatDump(t *testing.T) {
	script, err := winuiScript(GUIAction{Kind: "tree"})
	if err != nil {
		t.Fatalf("winuiScript tree: %v", err)
	}
	for _, want := range []string{"GetForegroundWindow", "FromHandle", "TreeScope]::Descendants"} {
		if !strings.Contains(script, want) {
			t.Errorf("tree script missing %q", want)
		}
	}
}
