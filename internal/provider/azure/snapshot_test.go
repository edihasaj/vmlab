package azure

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Default mode is OS-disk snapshot. The stub responds to:
//
//	vm show (resolves OS-disk id)
//	snapshot create (the actual capture)
//
// Anything else exits 1 so unexpected calls are loud.
func TestSnapshotDefaultModeCapturesOSDisk(t *testing.T) {
	dir := t.TempDir()
	args := stubAz(t, dir, `case "$1 $2" in
"vm show") echo "/subscriptions/x/resourceGroups/vmlab-rg/providers/Microsoft.Compute/disks/smoke-osdisk"; exit 0 ;;
"snapshot create") echo '{"id":"/subs/x/snap-1"}'; exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	if err := New().Snapshot(context.Background(), instance("smoke", nil), "v1", ""); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	raw, _ := os.ReadFile(args)
	got := string(raw)
	if !strings.Contains(got, "snapshot create") {
		t.Errorf("expected snapshot create call: %s", got)
	}
	if !strings.Contains(got, "vmlab-image=v1") {
		t.Errorf("expected vmlab-image tag: %s", got)
	}
}

func TestSnapshotImageModeUsesImageCreate(t *testing.T) {
	dir := t.TempDir()
	args := stubAz(t, dir, `case "$1 $2" in
"vm deallocate") exit 0 ;;
"image create") echo '{"id":"/subs/x/image-1"}'; exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	inst := instance("smoke", map[string]any{"snapshotMode": "image"})
	if err := New().Snapshot(context.Background(), inst, "v1", ""); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	raw, _ := os.ReadFile(args)
	got := string(raw)
	if !strings.Contains(got, "vm deallocate") {
		t.Errorf("image mode should deallocate first: %s", got)
	}
	if !strings.Contains(got, "image create") {
		t.Errorf("expected image create call: %s", got)
	}
}

func TestListSnapshotsCombinesSnapshotsAndImages(t *testing.T) {
	dir := t.TempDir()
	stubAz(t, dir, `case "$1 $2" in
"snapshot list") cat <<JSON
[{"Id":"/snap/1","Name":"vmlab-smoke-v1","Date":"2026-05-01T00:00:00Z","Tags":{"vmlab-image":"v1"},"State":"Succeeded"}]
JSON
exit 0 ;;
"image list") cat <<JSON
[{"Id":"/img/2","Name":"vmlab-smoke-v2","Date":"2026-05-02T00:00:00Z","Tags":{"vmlab-image":"v2"},"State":"Succeeded"}]
JSON
exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	snaps, err := New().ListSnapshots(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots (1 snap-mode + 1 image-mode), got %d: %+v", len(snaps), snaps)
	}
}

func TestDeleteSnapshotRemovesBothKinds(t *testing.T) {
	dir := t.TempDir()
	args := stubAz(t, dir, `case "$1 $2" in
"snapshot list") echo '["vmlab-smoke-v1"]'; exit 0 ;;
"image list")    echo '["vmlab-smoke-v1-image"]'; exit 0 ;;
"snapshot delete") exit 0 ;;
"image delete")    exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	if err := New().DeleteSnapshot(context.Background(), instance("smoke", nil), "v1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := os.ReadFile(args)
	if !strings.Contains(string(got), "snapshot delete") || !strings.Contains(string(got), "image delete") {
		t.Errorf("expected both snapshot+image delete calls: %s", got)
	}
}
