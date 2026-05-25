package gcp

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSnapshotCreatesMachineImage(t *testing.T) {
	dir := t.TempDir()
	args := stubGcloud(t, dir, `case "$3 $4 $5" in
"compute machine-images create") echo '{"name":"vmlab-smoke-v1"}'; exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	if err := New().Snapshot(context.Background(), instance("smoke", nil), "v1", ""); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	got, _ := os.ReadFile(args)
	s := string(got)
	if !strings.Contains(s, "compute machine-images create vmlab-smoke-v1") {
		t.Errorf("expected machine-images create with vmlab name: %s", s)
	}
	if !strings.Contains(s, "vmlab-image=v1") {
		t.Errorf("expected vmlab-image label: %s", s)
	}
}

func TestListSnapshotsParsesGcloudJSON(t *testing.T) {
	dir := t.TempDir()
	stubGcloud(t, dir, `cat <<JSON
[
  {"name":"vmlab-smoke-v1","creationTimestamp":"2026-05-01T00:00:00Z","status":"READY","labels":{"vmlab-image":"v1"}}
]
JSON
exit 0`)
	withPath(t, dir)

	snaps, err := New().ListSnapshots(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Name != "v1" {
		t.Fatalf("unexpected snaps: %+v", snaps)
	}
}

func TestDeleteSnapshotIteratesNames(t *testing.T) {
	dir := t.TempDir()
	args := stubGcloud(t, dir, `case "$4 $5" in
"machine-images list") printf 'vmlab-smoke-v1\n'; exit 0 ;;
"machine-images delete") exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	if err := New().DeleteSnapshot(context.Background(), instance("smoke", nil), "v1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := os.ReadFile(args)
	if !strings.Contains(string(got), "machine-images delete vmlab-smoke-v1") {
		t.Errorf("expected delete call: %s", got)
	}
}

func TestRestoreReturnsNotInPlace(t *testing.T) {
	err := New().Restore(context.Background(), instance("smoke", nil), "v1")
	if err == nil || !strings.Contains(err.Error(), "sourceMachineImage") {
		t.Fatalf("expected sourceMachineImage hint, got: %v", err)
	}
}
