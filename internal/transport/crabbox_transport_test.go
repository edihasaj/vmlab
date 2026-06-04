package transport

import (
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

func crabboxTarget(settings map[string]any) target.Target {
	return target.Target{
		Name:      "cbx",
		Transport: "crabbox",
		Settings:  map[string]any{"crabbox": settings},
	}
}

func TestCrabboxAddrByID(t *testing.T) {
	got := strings.Join(crabboxAddr(crabboxTarget(map[string]any{"id": "blue-lobster"})), " ")
	if got != "-id blue-lobster" {
		t.Fatalf("addr by id: got %q", got)
	}
}

func TestCrabboxAddrSlugFallsBackToID(t *testing.T) {
	got := strings.Join(crabboxAddr(crabboxTarget(map[string]any{"slug": "calm-otter"})), " ")
	if got != "-id calm-otter" {
		t.Fatalf("addr by slug: got %q", got)
	}
}

func TestCrabboxAddrStaticDefaultsProviderSSH(t *testing.T) {
	got := strings.Join(crabboxAddr(crabboxTarget(map[string]any{
		"host": "10.0.0.5", "user": "ci", "port": "2222",
	})), " ")
	want := "-static-host 10.0.0.5 -static-user ci -static-port 2222 -provider ssh"
	if got != want {
		t.Fatalf("static addr: got %q want %q", got, want)
	}
}

func TestCrabboxAddrProviderRespected(t *testing.T) {
	got := strings.Join(crabboxAddr(crabboxTarget(map[string]any{
		"id": "x1", "provider": "hetzner",
	})), " ")
	if got != "-id x1 -provider hetzner" {
		t.Fatalf("addr with provider: got %q", got)
	}
}

// Run flags must follow the subcommand and precede the `--` separator —
// the v0.21 crabbox CLI rejects flags placed before the subcommand.
func TestCrabboxRunArgsOrdering(t *testing.T) {
	got := crabboxRunArgs(crabboxTarget(map[string]any{"id": "x1"}), []string{"pnpm", "test"})
	want := []string{"run", "-id", "x1", "--", "pnpm", "test"}
	if len(got) != len(want) {
		t.Fatalf("run args: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("run args[%d]: got %q want %q (%v)", i, got[i], want[i], got)
		}
	}
}

func TestCrabboxHasLeaseAddr(t *testing.T) {
	if hasLeaseAddr(crabboxTarget(map[string]any{"provider": "hetzner"})) {
		t.Error("provider-only target should not count as a lease addr")
	}
	if !hasLeaseAddr(crabboxTarget(map[string]any{"id": "x1"})) {
		t.Error("id target should count as a lease addr")
	}
}

func TestCrabboxCapsAdvertisesScreenshot(t *testing.T) {
	c := NewCrabbox()
	caps := c.Capabilities()
	if !caps.Screenshot {
		t.Error("crabbox should advertise Screenshot (crabbox screenshot is supported)")
	}
	if caps.GUI {
		t.Error("crabbox should not advertise GUI driving (guiport owns that)")
	}
}
