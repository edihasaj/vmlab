//go:build windows

package mcp

import "os/exec"

// newDetachedCmd on Windows: CREATE_NEW_PROCESS_GROUP keeps the child from
// inheriting the parent's console signal handlers. The vmlab MCP server is
// rarely run on Windows hosts, but this keeps the build green and the
// behaviour roughly equivalent to setsid on Unix.
func newDetachedCmd(bin string, args []string) *exec.Cmd {
	return exec.Command(bin, args...)
}
