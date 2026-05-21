package flow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// runArtifactStep executes one `artifact:` step on the host. Returns the
// command picked for this OS (for logging) plus any exec/cache error.
// stdout/stderr stream the build's output so users see the compiler.
//
// Cache key includes: src content fingerprint + build cmd + osKind +
// arch. A hit means we've already built this exact input on this host;
// the build script is skipped but delivery still happens so a fresh VM
// gets the binary even on a cache hit.
//
// When spec.Output[osKind] and spec.DeliverTo are both set, the picked
// output file is pushed into the target via tr.Sync after a successful
// build (cached or fresh). For ssh / ssh-windows transports, DeliverTo
// is wired into the target's ssh.dest setting on a cloned target so the
// original config stays untouched.
func runArtifactStep(ctx context.Context, spec *ArtifactSpec, osKind, arch, cacheDir string, tr transport.Transport, tgt target.Target, stdout, stderr io.Writer) (string, bool, error) {
	cmdLine, ok := pickInstall(spec.Build, osKind) // same alias logic as install dispatch
	if !ok {
		return "", false, nil
	}

	srcHash, err := hashSourceTree(spec.Src)
	if err != nil {
		return cmdLine, false, fmt.Errorf("hash %s: %w", spec.Src, err)
	}
	key := artifactCacheKey(srcHash, cmdLine, osKind, arch)

	cached := false
	if cacheDir != "" {
		hit, herr := cacheHit(cacheDir, key)
		if herr == nil && hit {
			fmt.Fprintf(stderr, "artifact: cache hit (%s) — skipping build\n", short12(key))
			cached = true
		}
	}

	if !cached {
		// Pick a host-side shell. We stay agnostic about the user's build
		// toolchain — they own quoting / env / cd inside the command.
		hostShell := []string{"sh", "-lc", cmdLine}
		if runtimeIsWindowsHost() {
			hostShell = []string{"powershell", "-NoProfile", "-Command", cmdLine}
		}
		c := exec.CommandContext(ctx, hostShell[0], hostShell[1:]...)
		c.Stdout = stdout
		c.Stderr = stderr
		if err := c.Run(); err != nil {
			return cmdLine, false, err
		}
		if cacheDir != "" {
			_ = writeCacheEntry(cacheDir, key, srcHash, cmdLine, osKind, arch)
		}
	}

	if err := deliverArtifact(ctx, spec, osKind, tr, tgt, stderr); err != nil {
		return cmdLine, cached, err
	}
	return cmdLine, cached, nil
}

// deliverArtifact pushes the build output into the target via tr.Sync when
// both an output path and a deliver_to path are configured. No-op when
// either is empty so build-only flows stay supported.
//
// For ssh / ssh-windows the deliver_to value is wired into a cloned
// target's ssh.dest setting. For other transports we fall back to plain
// Sync (parallels-guest uses shared folders, so users on that path should
// keep using mounts / vmlab sync rather than artifact deliver_to).
func deliverArtifact(ctx context.Context, spec *ArtifactSpec, osKind string, tr transport.Transport, tgt target.Target, stderr io.Writer) error {
	if spec.DeliverTo == "" {
		return nil
	}
	out, ok := pickInstall(spec.Output, osKind)
	if !ok || out == "" {
		// No output mapped for this OS → silently skip; users describe a
		// subset of OSes that get a build+deliver via `output:` and the
		// rest are run/exec-only steps.
		return nil
	}
	if tr == nil {
		return fmt.Errorf("artifact deliver_to: no transport available (artifact step must run inside a real run, not a dry-run)")
	}
	if _, err := os.Stat(out); err != nil {
		return fmt.Errorf("artifact output %q missing after build: %w", out, err)
	}
	delivered := tgt
	delivered.Settings = cloneSettingsWithDest(tgt.Settings, tgt.Transport, spec.DeliverTo)
	fmt.Fprintf(stderr, "artifact: deliver %s → %s:%s\n", out, tgt.Name, spec.DeliverTo)
	return tr.Sync(ctx, delivered, out)
}

// cloneSettingsWithDest deep-copies the target settings map and sets the
// transport-appropriate dest path. We mutate only the namespace the
// transport reads from so unrelated settings (timeouts, keys, etc.) keep
// flowing through unchanged.
func cloneSettingsWithDest(src map[string]any, transportName, dest string) map[string]any {
	out := make(map[string]any, len(src)+1)
	for k, v := range src {
		if m, ok := v.(map[string]any); ok {
			cp := make(map[string]any, len(m))
			for kk, vv := range m {
				cp[kk] = vv
			}
			out[k] = cp
		} else {
			out[k] = v
		}
	}
	// ssh + ssh-windows both consume ssh.dest.
	if transportName == "ssh" || transportName == "ssh-windows" {
		sshMap, _ := out["ssh"].(map[string]any)
		if sshMap == nil {
			sshMap = map[string]any{}
		}
		sshMap["dest"] = dest
		out["ssh"] = sshMap
	}
	return out
}

// hashSourceTree mirrors the watch hash but kept in the flow package so we
// don't introduce a cli → flow dep. Includes path, mtime nanos, size for
// each file under src (skipping hidden dirs). Empty src returns an empty
// hash so artifact: { src: "" } still works — the build cmd alone keys the
// cache.
func hashSourceTree(src string) (string, error) {
	if strings.TrimSpace(src) == "" {
		return "", nil
	}
	h := sha256.New()
	type entry struct {
		path string
		mod  int64
		size int64
	}
	var entries []entry
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		entries = append(entries, entry{src, info.ModTime().UnixNano(), info.Size()})
	} else {
		walkErr := filepath.WalkDir(src, func(sub string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if sub != src && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			entries = append(entries, entry{sub, fi.ModTime().UnixNano(), fi.Size()})
			return nil
		})
		if walkErr != nil {
			return "", walkErr
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	for _, e := range entries {
		fmt.Fprintf(h, "%s\t%d\t%d\n", e.path, e.mod, e.size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// artifactCacheKey derives a deterministic cache file name.
func artifactCacheKey(srcHash, cmd, osKind, arch string) string {
	h := sha256.New()
	fmt.Fprintf(h, "src=%s\nos=%s\narch=%s\ncmd=%s\n", srcHash, osKind, arch, cmd)
	return hex.EncodeToString(h.Sum(nil))
}

// cacheRecord is what we serialise under <cacheDir>/<key>.json — useful
// when debugging "why did this rebuild?".
type cacheRecord struct {
	Key     string `json:"key"`
	SrcHash string `json:"src_hash"`
	Cmd     string `json:"cmd"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Built   string `json:"built"` // RFC3339
}

func cacheHit(cacheDir, key string) (bool, error) {
	if cacheDir == "" {
		return false, nil
	}
	path := filepath.Join(cacheDir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var r cacheRecord
	if err := json.Unmarshal(data, &r); err != nil {
		// Corrupt cache file → treat as miss so the next build rewrites it.
		return false, nil
	}
	return r.Key == key, nil
}

func writeCacheEntry(cacheDir, key, srcHash, cmd, osKind, arch string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	r := cacheRecord{
		Key: key, SrcHash: srcHash, Cmd: cmd, OS: osKind, Arch: arch,
		Built: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, key+".json"), data, 0o644)
}

// artifactCacheDir returns ~/.vmlab/cache/artifacts (created lazily). If
// HOME can't be resolved we return "" so artifact builds still work — they
// just don't get cached. Overridable via VMLAB_ARTIFACT_CACHE for tests.
func artifactCacheDir() string {
	if v := os.Getenv("VMLAB_ARTIFACT_CACHE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".vmlab", "cache", "artifacts")
}

// runtimeIsWindowsHost reports whether the host running vmlab is Windows.
// Kept as a tiny indirection so tests can stub it via swapping the var.
var runtimeIsWindowsHost = func() bool { return os.PathSeparator == '\\' }

func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
