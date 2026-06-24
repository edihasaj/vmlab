package transport

import (
	"strings"

	"github.com/edihasaj/vmlab/internal/target"
)

// WrapShell wraps a single command line for execution on the target's OS so
// callers can hand the result straight to Transport.Run. The previous
// hard-coded ["sh","-lc",cmd] was wrong for windows guests (parallels-guest /
// ssh-windows) — there is no `sh` there, so prlctl/ssh returned exit=2 with
// empty output.
//
// Choice order:
//  1. target.OSKind() == "windows" → cmd.exe /c by default; ssh.shell hint
//     flips to PowerShell — "pwsh" → pwsh.exe (PowerShell 7+, supports `&&`),
//     "powershell" → powershell.exe (Windows PowerShell 5.1).
//  2. otherwise → posix sh -lc.
//
// pwsh.exe and powershell.exe are different binaries with different syntax
// support; wrapForExec emits the env/workdir prefix that matches the choice.
func WrapShell(t target.Target, cmdLine string) []string {
	if t.OSKind() == "windows" {
		switch strings.ToLower(strings.TrimSpace(t.SettingString("ssh", "shell"))) {
		case "pwsh":
			return []string{"pwsh.exe", "-NoProfile", "-Command", cmdLine}
		case "powershell":
			return []string{"powershell.exe", "-NoProfile", "-Command", cmdLine}
		}
		return []string{"cmd.exe", "/c", cmdLine}
	}
	return []string{"sh", "-lc", cmdLine}
}

// IsHostShellArgv reports whether argv looks like `sh -lc <cmd>` — the
// shape WrapShell emits for run:/assert: steps. Transports that route
// argv to a tool's subcommand (adb, simctl, idb, maestro) use this to
// detect run:/assert: steps and execute them on the host instead.
func IsHostShellArgv(argv []string) bool {
	return len(argv) >= 3 && argv[0] == "sh" && argv[1] == "-lc"
}
