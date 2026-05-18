package azure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
)

// stubAz writes a fake `az` binary that records its argv to azArgsFile and
// runs the supplied shell body. Returns the path to the args log so tests
// can assert which calls were made.
func stubAz(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	argsFile := filepath.Join(dir, "az.args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
%s
`, argsFile, body)
	path := filepath.Join(dir, "az")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return argsFile
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func instance(name string, extra map[string]any) provider.Instance {
	if extra == nil {
		extra = map[string]any{}
	}
	if _, ok := extra["resourceGroup"]; !ok {
		extra["resourceGroup"] = "vmlab-rg"
	}
	return provider.Instance{
		Name:     name,
		Provider: "azure",
		Settings: map[string]any{"azure": extra},
	}
}

func TestStatusRunning(t *testing.T) {
	dir := t.TempDir()
	stubAz(t, dir, `cat <<JSON
{"powerState":"VM running","publicIps":"20.10.10.5"}
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st != provider.StateRunning {
		t.Errorf("state=%v", st)
	}
}

func TestStatusDeallocatedMapsToStopped(t *testing.T) {
	dir := t.TempDir()
	stubAz(t, dir, `cat <<JSON
{"powerState":"VM deallocated","publicIps":""}
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st != provider.StateStopped {
		t.Errorf("expected stopped, got %v", st)
	}
}

func TestStatusNotFound(t *testing.T) {
	dir := t.TempDir()
	stubAz(t, dir, `echo "ResourceNotFound: VM 'ghost' not found"; exit 3`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("ghost", nil))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st != provider.StateNotFound {
		t.Errorf("state=%v", st)
	}
}

func TestCreateInvokesVmCreate(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAz(t, dir, `case "$*" in
  *"vm create"*) echo '{"id":"/subs/x/rg/vmlab-rg/vm/smoke"}' ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().create(context.Background(), instance("smoke", map[string]any{
		"image":    "Ubuntu2404",
		"size":     "Standard_B1s",
		"location": "westeurope",
	})); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"vm create", "-g vmlab-rg", "-n smoke", "--image Ubuntu2404",
		"--size Standard_B1s", "--location westeurope", "--tags vmlab=smoke"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in args:\n%s", want, got)
		}
	}
}

func TestDownDeallocateSuspendCallsCorrectVerb(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAz(t, dir, `case "$*" in
  *"vm show"*) cat <<JSON
{"powerState":"VM running","publicIps":"1.2.3.4"}
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeSuspend)
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "vm deallocate") {
		t.Fatalf("expected `vm deallocate` call; got:\n%s", got)
	}
}

func TestDownDestroyCallsDelete(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAz(t, dir, `case "$*" in
  *"vm show"*) cat <<JSON
{"powerState":"VM running","publicIps":"1.2.3.4"}
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeDestroy); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "vm delete") || !strings.Contains(string(got), "--yes") {
		t.Fatalf("expected `vm delete ... --yes`; got:\n%s", got)
	}
}

func TestDoctorReportsSubscription(t *testing.T) {
	dir := t.TempDir()
	stubAz(t, dir, `cat <<JSON
{"name":"Dev Subscription","id":"sub-uuid-123"}
JSON
exit 0`)
	withPath(t, dir)

	h := New().Doctor(context.Background(), instance("smoke", nil))
	if !h.OK {
		t.Fatalf("expected OK; got %+v", h)
	}
	if !strings.Contains(h.Message, "Dev Subscription") {
		t.Fatalf("missing subscription in message: %q", h.Message)
	}
}

func TestDoctorMissingResourceGroupFails(t *testing.T) {
	dir := t.TempDir()
	stubAz(t, dir, `exit 0`)
	withPath(t, dir)

	i := provider.Instance{Name: "smoke", Provider: "azure", Settings: map[string]any{"azure": map[string]any{}}}
	h := New().Doctor(context.Background(), i)
	if h.OK {
		t.Fatalf("expected failure for missing resourceGroup")
	}
}

func TestSubscriptionFlagPrepended(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAz(t, dir, `exit 0`)
	withPath(t, dir)

	_, _ = New().run(context.Background(),
		instance("smoke", map[string]any{"subscription": "sub-A"}),
		"vm", "show", "-g", "rg", "-n", "smoke")
	got, _ := os.ReadFile(argsFile)
	first := strings.SplitN(strings.TrimSpace(string(got)), " ", 4)
	if len(first) < 3 || first[0] != "--subscription" || first[1] != "sub-A" {
		t.Fatalf("subscription flag not prepended: %q", got)
	}
}
