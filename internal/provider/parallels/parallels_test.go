package parallels

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/edihasaj/vmlab/internal/provider"
)

func stubPrlctl(t *testing.T, dir string, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	argsFile := filepath.Join(dir, "prlctl.args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
%s
`, argsFile, body)
	path := filepath.Join(dir, "prlctl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return argsFile
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestParallelsStatusRunning(t *testing.T) {
	dir := t.TempDir()
	stubPrlctl(t, dir, `case "$1" in
status) echo 'VM "Windows 11" exist running'; exit 0 ;;
esac`)
	withPath(t, dir)

	p := New()
	inst := provider.Instance{
		Name:     "win11",
		Provider: "parallels",
		Settings: map[string]any{"parallels": map[string]any{"vm": "Windows 11"}},
	}
	st, err := p.Status(context.Background(), inst)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st != provider.StateRunning {
		t.Errorf("state=%v, want running", st)
	}
}

func TestParallelsStatusSuspended(t *testing.T) {
	dir := t.TempDir()
	stubPrlctl(t, dir, `echo 'VM "x" exist suspended'; exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st != provider.StateSuspended {
		t.Errorf("state=%v", st)
	}
}

func TestParallelsUpStartsIfSuspended(t *testing.T) {
	dir := t.TempDir()
	args := stubPrlctl(t, dir, `# return suspended first, running after start
case "$1" in
status)
  if [ -f "`+dir+`/started" ]; then echo 'VM "x" exist running'; else echo 'VM "x" exist suspended'; fi
  exit 0
  ;;
start)
  touch "`+dir+`/started"; exit 0
  ;;
exec)
  exit 0
  ;;
esac`)
	withPath(t, dir)

	p := New()
	inst := provider.Instance{
		Name:     "x",
		Provider: "parallels",
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
		Ready:    provider.ReadyConfig{Kind: "parallels-tools", Timeout: "5s"},
	}
	_, res, err := p.Up(context.Background(), inst)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if !res.Changed {
		t.Errorf("expected Changed=true")
	}
	if res.PriorState != provider.StateSuspended {
		t.Errorf("prior=%v", res.PriorState)
	}
	data, _ := os.ReadFile(args)
	if !strings.Contains(string(data), "start x") {
		t.Errorf("expected `start x` in args, got: %s", data)
	}
}

func TestParallelsUpNoOpIfRunning(t *testing.T) {
	dir := t.TempDir()
	stubPrlctl(t, dir, `case "$1" in
status) echo 'VM "x" exist running'; exit 0 ;;
exec) exit 0 ;;
esac`)
	withPath(t, dir)

	_, res, err := New().Up(context.Background(), provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
		Ready:    provider.ReadyConfig{Timeout: "5s"},
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.Changed {
		t.Errorf("expected Changed=false when already running")
	}
}

func TestParallelsDownSuspend(t *testing.T) {
	dir := t.TempDir()
	args := stubPrlctl(t, dir, `case "$1" in
status) echo 'VM "x" exist running'; exit 0 ;;
suspend) exit 0 ;;
esac`)
	withPath(t, dir)

	err := New().Down(context.Background(), provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
	}, provider.DisposeSuspend)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	data, _ := os.ReadFile(args)
	if !strings.Contains(string(data), "suspend x") {
		t.Errorf("expected `suspend x`, got: %s", data)
	}
}

func TestParallelsDownKeepIsNoOp(t *testing.T) {
	dir := t.TempDir()
	args := stubPrlctl(t, dir, `echo 'VM "x" exist running'; exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
	}, provider.DisposeKeep); err != nil {
		t.Fatal(err)
	}
	// keep => Down should not call prlctl at all.
	if _, err := os.Stat(args); err == nil {
		data, _ := os.ReadFile(args)
		t.Errorf("expected no prlctl invocations, got: %s", data)
	}
}

func TestParseStatus(t *testing.T) {
	cases := map[string]provider.State{
		"VM \"x\" exist running":   provider.StateRunning,
		"VM \"x\" exist suspended": provider.StateSuspended,
		"VM \"x\" exist stopped":   provider.StateStopped,
		"VM \"x\" exist paused":    provider.StateSuspended,
		"weird":                    provider.StateUnknown,
	}
	for in, want := range cases {
		if got := parseStatus(in); got != want {
			t.Errorf("parseStatus(%q)=%v, want %v", in, got, want)
		}
	}
}

func TestParseSnapshotList(t *testing.T) {
	out := `{
	"{4a78327a-13b8-4844-9f00-b8c68f6523b0}": {
	"name": "clean",
	"date": "2026-05-12 23:24:38",
	"state": "poweron",
	"current": true,
	"parent": ""
}

}`
	snaps, err := parseSnapshotList(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("want 1, got %d", len(snaps))
	}
	s := snaps[0]
	if s.Name != "clean" {
		t.Errorf("name=%q", s.Name)
	}
	if !s.Current {
		t.Errorf("expected current=true")
	}
	if s.ID == "" || s.ID[0] != '{' {
		t.Errorf("id=%q (expected braced UUID)", s.ID)
	}
}

func TestParseSnapshotListEmpty(t *testing.T) {
	snaps, err := parseSnapshotList("")
	if err != nil || snaps != nil {
		t.Errorf("empty: snaps=%+v err=%v", snaps, err)
	}
	snaps, err = parseSnapshotList("{}")
	if err != nil || snaps != nil {
		t.Errorf("empty-obj: snaps=%+v err=%v", snaps, err)
	}
}

func TestEnsureMountsIdempotent(t *testing.T) {
	dir := t.TempDir()
	args := stubPrlctl(t, dir, `case "$1" in
status) echo 'VM "x" exist running'; exit 0 ;;
exec) exit 0 ;;
set)
  # already-used flag exits non-zero with the canonical message
  if [ "$5" = "exists" ]; then
    echo "The shared folder name '$5' already used for VM" 1>&2
    exit 255
  fi
  echo "Creating shared folder: $5"
  exit 0 ;;
esac`)
	withPath(t, dir)

	p := New()
	inst := provider.Instance{
		Name:     "x",
		Provider: "parallels",
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
		Ready:    provider.ReadyConfig{Timeout: "2s"},
		Mounts: []provider.Mount{
			{Name: "fresh", Host: dir},
			{Name: "exists", Host: dir},
		},
	}
	_, _, err := p.Up(context.Background(), inst)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	data, _ := os.ReadFile(args)
	if !strings.Contains(string(data), "--shf-host-add fresh --path "+dir) {
		t.Errorf("expected fresh add in args: %s", data)
	}
	if !strings.Contains(string(data), "--shf-host-add exists --path "+dir) {
		t.Errorf("expected exists add attempt: %s", data)
	}
}

func TestSnapshotIDLookup(t *testing.T) {
	dir := t.TempDir()
	stubPrlctl(t, dir, `case "$1" in
snapshot-list) cat <<JSON
{
  "{aaa}": {"name":"alpha","date":"","state":"poweron","current":true,"parent":""},
  "{bbb}": {"name":"beta","date":"","state":"poweron","current":false,"parent":""}
}
JSON
  exit 0 ;;
esac`)
	withPath(t, dir)
	p := New()
	id, err := p.snapshotID(context.Background(), provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "x"}},
	}, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if id != "{bbb}" {
		t.Errorf("id=%q", id)
	}
}

func TestPosixQuote(t *testing.T) {
	if got := posixQuote("Windows 11"); got != `'Windows 11'` {
		t.Errorf("got %q", got)
	}
}

func TestWaitReadyTimeout(t *testing.T) {
	dir := t.TempDir()
	stubPrlctl(t, dir, `case "$1" in
status) echo 'VM "x" exist running'; exit 0 ;;
exec) exit 1 ;;
esac`)
	withPath(t, dir)

	p := New()
	inst := provider.Instance{
		Settings: provider.Instance{}.Settings,
		Ready:    provider.ReadyConfig{Timeout: "1s"},
	}
	inst.Settings = map[string]any{"parallels": map[string]any{"vm": "x"}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err := p.Up(ctx, inst)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("unexpected err: %v", err)
	}
}

// stubOpen writes a fake `open` onto PATH that touches a marker so a paired
// prlctl stub can flip from "service down" to "service up" once it has run.
func stubOpen(t *testing.T, dir, marker string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\ntouch %q\nexit 0\n", marker)
	if err := os.WriteFile(filepath.Join(dir, "open"), []byte(script), 0o755); err != nil {
		t.Fatalf("write open stub: %v", err)
	}
}

func TestParallelsRestart(t *testing.T) {
	dir := t.TempDir()
	args := stubPrlctl(t, dir, `case "$1" in
restart) exit 0 ;;
exec) exit 0 ;;
esac`)
	withPath(t, dir)

	inst := provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "Windows 11"}},
		Ready:    provider.ReadyConfig{Timeout: "5s"},
	}
	if err := New().Restart(context.Background(), inst); err != nil {
		t.Fatalf("restart: %v", err)
	}
	data, _ := os.ReadFile(args)
	if !strings.Contains(string(data), "restart Windows 11") {
		t.Errorf("expected `restart Windows 11` in args, got: %s", data)
	}
}

// When `prlctl restart` fails (graceful reboot canceled by a wedged guest),
// Restart must hard-cycle: stop --kill then start, then wait for ready.
func TestParallelsRestartFallsBackToHardCycle(t *testing.T) {
	dir := t.TempDir()
	args := stubPrlctl(t, dir, `case "$1" in
restart) echo 'Failed to restart the VM: Operation canceled.' >&2; exit 255 ;;
stop) exit 0 ;;
start) exit 0 ;;
exec) exit 0 ;;
esac`)
	withPath(t, dir)

	inst := provider.Instance{
		Settings: map[string]any{"parallels": map[string]any{"vm": "Windows 11"}},
		Ready:    provider.ReadyConfig{Timeout: "5s"},
	}
	if err := New().Restart(context.Background(), inst); err != nil {
		t.Fatalf("restart fallback: %v", err)
	}
	got := string(mustRead(t, args))
	for _, want := range []string{"stop Windows 11 --kill", "start Windows 11"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in args, got: %s", want, got)
		}
	}
}

// Local mode should find prlctl at parallels.prlctlPath even when it isn't on
// PATH (non-login shell), so callers don't need `zsh -lc`.
func TestParallelsLocalFindsPrlctlViaPrlctlPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	bundle := t.TempDir() // stands in for the app-bundle MacOS dir; NOT on PATH
	script := "#!/bin/sh\necho 'VM \"x\" exist running'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bundle, "prlctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write prlctl: %v", err)
	}
	t.Setenv("PATH", "/nonexistent-vmlab-test") // ensure bare prlctl is unresolvable

	inst := provider.Instance{Settings: map[string]any{"parallels": map[string]any{
		"vm": "x", "prlctlPath": bundle,
	}}}
	st, err := New().Status(context.Background(), inst)
	if err != nil {
		t.Fatalf("status via prlctlPath fallback: %v", err)
	}
	if st != provider.StateRunning {
		t.Errorf("state=%v, want running", st)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// When prlctl reports the Parallels Service is down, runPrlctl should launch
// the app (our `open` stub flips the marker) and retry, so Status succeeds.
func TestParallelsAutoStartsServiceOnConnectFailure(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	stubPrlctl(t, dir, `if [ ! -f "`+marker+`" ]; then
  echo 'Login failed: Unable to connect to Parallels Service.' >&2
  exit 1
fi
case "$1" in
--version) echo 'prlctl version 19'; exit 0 ;;
status) echo 'VM "x" exist running'; exit 0 ;;
esac`)
	stubOpen(t, dir, marker)
	withPath(t, dir)

	inst := provider.Instance{Settings: map[string]any{"parallels": map[string]any{"vm": "x"}}}
	st, err := New().Status(context.Background(), inst)
	if err != nil {
		t.Fatalf("status after auto-start: %v", err)
	}
	if st != provider.StateRunning {
		t.Errorf("state=%v, want running", st)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("expected the app to be launched (marker missing): %v", err)
	}
}

// autostart=false must keep the old fail-fast behaviour (no app launch).
func TestParallelsAutoStartOptOut(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	stubPrlctl(t, dir, `echo 'Unable to connect to Parallels Service.' >&2; exit 1`)
	stubOpen(t, dir, marker)
	withPath(t, dir)

	inst := provider.Instance{Settings: map[string]any{"parallels": map[string]any{"vm": "x", "autostart": "false"}}}
	if _, err := New().Status(context.Background(), inst); err == nil {
		t.Fatal("expected error when autostart is disabled")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("app should NOT be launched when autostart=false")
	}
}
