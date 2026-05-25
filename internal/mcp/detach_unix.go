//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// newDetachedCmd builds an exec.Cmd that, when started, runs in its own
// session so a parent SIGHUP (or the MCP server exiting cleanly) does not
// propagate. Setsid is the simplest portable Unix recipe: the child becomes
// a session leader and inherits no controlling terminal.
func newDetachedCmd(bin string, args []string) *exec.Cmd {
	c := exec.Command(bin, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return c
}
