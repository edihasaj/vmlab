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
//     ("pwsh"/"powershell") flips to PowerShell.
//  2. otherwise → posix sh -lc.
func WrapShell(t target.Target, cmdLine string) []string {
	if t.OSKind() == "windows" {
		switch strings.ToLower(strings.TrimSpace(t.SettingString("ssh", "shell"))) {
		case "pwsh", "powershell":
			return []string{"powershell.exe", "-NoProfile", "-Command", cmdLine}
		}
		return []string{"cmd.exe", "/c", cmdLine}
	}
	// Android's /system/bin/sh mis-parses `-lc` — the `-l` flag eats the
	// script-name positional, so `sh -lc 'getprop X'` runs getprop with
	// no argument. adb shell already routes through the device's shell,
	// so we pass the command verbatim and let the transport's `adb shell`
	// invocation handle it.
	if t.Transport == "adb" {
		return []string{cmdLine}
	}
	return []string{"sh", "-lc", cmdLine}
}
