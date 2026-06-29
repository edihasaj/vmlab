package transport

import (
	"os"
	"path/filepath"

	"github.com/edihasaj/vmlab/internal/target"
)

// SSH connection multiplexing (OpenSSH ControlMaster).
//
// Without this, vmlab spawns the system `ssh` binary for every `run` / `cp` /
// `doctor`, and each spawn pays a full TCP + SSH handshake — ~1.5s of latency
// even for a trivial command. ControlMaster makes the first connection to a host
// open a persistent master socket; every later connection to the same host rides
// that socket (just a new channel), cutting per-call latency ~4-5x. It is the
// transport-layer analogue of keeping one browser/CDP session warm.
//
// Enabled by default for every ssh-based transport. A target opts out with
// `ssh.multiplex: false`. The master lingers `ssh.controlPersist` (default 10m)
// after the last channel closes, then exits on its own.

const defaultControlPersist = "10m"

// sshMultiplexArgs returns the `-o ControlMaster …` argv fragment to splice into
// an `ssh` invocation, or nil when multiplexing is disabled / unavailable (in
// which case ssh just behaves as before).
func sshMultiplexArgs(t target.Target) []string {
	dir, persist, ok := sshMultiplexConfig(t)
	if !ok {
		return nil
	}
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(dir, "%C"),
		"-o", "ControlPersist=" + persist,
	}
}

// sshMultiplexOptionString returns the same flags as a single space-joined
// string, for embedding in an rsync `-e "ssh …"` remote-shell argument. Empty
// when multiplexing is disabled.
func sshMultiplexOptionString(t target.Target) string {
	dir, persist, ok := sshMultiplexConfig(t)
	if !ok {
		return ""
	}
	return " -o ControlMaster=auto -o ControlPath=" + filepath.Join(dir, "%C") +
		" -o ControlPersist=" + persist
}

// sshMultiplexConfig resolves whether multiplexing is on plus the socket dir and
// persist window. ok is false when the target opted out or no socket dir could
// be created (we then fall back to plain, unmultiplexed ssh rather than error).
func sshMultiplexConfig(t target.Target) (dir, persist string, ok bool) {
	if t.Setting("ssh", "multiplex") == false { // unset (nil) keeps it enabled
		return "", "", false
	}
	dir = sshControlDir()
	if dir == "" {
		return "", "", false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", false
	}
	persist = t.SettingString("ssh", "controlPersist")
	if persist == "" {
		persist = defaultControlPersist
	}
	// %C is a short hash of (local host, remote host, port, user); it keeps the
	// socket path well under the ~104-char unix-socket limit on macOS.
	return dir, persist, true
}

// sshControlDir is the directory holding ControlPath sockets: <VMLAB_HOME>/cm,
// or ~/.vmlab/cm. Returns "" if no home can be resolved.
func sshControlDir() string {
	if h := os.Getenv("VMLAB_HOME"); h != "" {
		return filepath.Join(h, "cm")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vmlab", "cm")
}
