package flow

import (
	"reflect"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

func TestSubstitute_AllSyntaxes(t *testing.T) {
	rt := newRuntime(target.Target{Name: "win", Transport: "parallels-guest"})
	rt.set("VMLAB_SYNC_DIR", `\\Mac\recall`)
	cases := map[string]string{
		`cd $VMLAB_SYNC_DIR && build`:  `cd \\Mac\recall && build`,
		`cd ${VMLAB_SYNC_DIR}/sub`:     `cd \\Mac\recall/sub`,
		`pushd "%VMLAB_SYNC_DIR%"`:     `pushd "\\Mac\recall"`,
		`$VMLAB_TARGET on $VMLAB_OS`:   `win on windows`,
		`literal $ sign with no var`:   `literal $ sign with no var`,
		`unknown $NOPE stays literal`:  `unknown $NOPE stays literal`,
		`powershell $i preserved`:      `powershell $i preserved`,
		`mixed $VMLAB_OS / %VMLAB_OS%`: `mixed windows / windows`,
	}
	for in, want := range cases {
		if got := rt.substitute(in); got != want {
			t.Errorf("substitute(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWrapForExec_WindowsUsesPushdAndSet(t *testing.T) {
	tgt := target.Target{Name: "w", Transport: "parallels-guest"}
	got := wrapForExec(tgt, "pnpm install", `\\Mac\recall`, map[string]string{"NODE_ENV": "production"})
	want := `set "NODE_ENV=production" && pushd "\\Mac\recall" && pnpm install`
	if got != want {
		t.Fatalf("windows wrap mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestWrapForExec_PosixUsesExportAndCd(t *testing.T) {
	tgt := target.Target{Name: "u", Transport: "ssh"}
	got := wrapForExec(tgt, "make test", "/srv/src", map[string]string{"CGO": "0"})
	want := `export CGO='0' && cd '/srv/src' && make test`
	if got != want {
		t.Fatalf("posix wrap mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestWrapForExec_NoWorkdirNoEnvIsPassThrough(t *testing.T) {
	tgt := target.Target{Name: "u", Transport: "ssh"}
	if got := wrapForExec(tgt, "pwd", "", nil); got != "pwd" {
		t.Fatalf("expected pass-through, got %q", got)
	}
}

func TestMergedEnv_StepOverridesFlow(t *testing.T) {
	rt := newRuntime(target.Target{Name: "x", Transport: "ssh"})
	rt.set("VMLAB_SYNC_DIR", "/staged")
	got := mergedEnv(
		map[string]string{"PATH": "/usr/bin", "OWNER": "flow"},
		map[string]string{"OWNER": "step", "WHERE": "$VMLAB_SYNC_DIR"},
		rt,
	)
	want := map[string]string{"PATH": "/usr/bin", "OWNER": "step", "WHERE": "/staged"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergedEnv mismatch\n got: %v\nwant: %v", got, want)
	}
}
