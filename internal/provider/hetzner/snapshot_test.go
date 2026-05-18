package hetzner

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
)

func TestSnapshotInvokesCreateImage(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubHcloud(t, dir, `exit 0`)
	withPath(t, dir)
	t.Setenv("HCLOUD_TOKEN", "test")

	err := New().Snapshot(context.Background(),
		instance("smoke", map[string]any{}),
		"clean-base", "first cut")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"server create-image smoke", "--type snapshot",
		"--description first cut", "--label vmlab-image=clean-base", "--label vmlab-source=smoke"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in args:\n%s", want, got)
		}
	}
}

func TestListSnapshotsParsesLabelFilter(t *testing.T) {
	dir := t.TempDir()
	stubHcloud(t, dir, `cat <<JSON
[{"id":42,"name":"vmlab-clean-base","created":"2026-05-18T10:00:00Z","description":"first cut","labels":{"vmlab-image":"clean-base"}}]
JSON
exit 0`)
	withPath(t, dir)
	t.Setenv("HCLOUD_TOKEN", "test")

	snaps, err := New().ListSnapshots(context.Background(), instance("smoke", map[string]any{}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Name != "clean-base" || snaps[0].ID != "42" {
		t.Fatalf("unexpected snaps: %+v", snaps)
	}
}

func TestDeleteSnapshotByLabel(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubHcloud(t, dir, `case "$*" in
  *"image list"*) cat <<JSON
[{"id":17},{"id":42}]
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)
	t.Setenv("HCLOUD_TOKEN", "test")

	if err := New().DeleteSnapshot(context.Background(),
		instance("smoke", map[string]any{}),
		"clean-base"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"image list -l vmlab-image=clean-base", "image delete 17", "image delete 42"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in args:\n%s", want, got)
		}
	}
}

// ensure provider.Snapshotter is satisfied
var _ provider.Snapshotter = (*Provider)(nil)
