package aws

import (
	"context"
	"os"
	"strings"
	"testing"
)

// describe-instances is used by Snapshot to resolve the instance id, then
// create-image fires. The stub responds to both based on the first arg.
func TestSnapshotConstructsCreateImage(t *testing.T) {
	dir := t.TempDir()
	args := stubAws(t, dir, `case "$2" in
describe-instances) echo '[{"Id":"i-deadbeef","State":"running","PublicIp":"1.2.3.4"}]'; exit 0 ;;
create-image) echo '{"ImageId":"ami-xyz"}'; exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	if err := New().Snapshot(context.Background(), instance("smoke", nil), "v1", "first snap"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	raw, _ := os.ReadFile(args)
	got := string(raw)
	if !strings.Contains(got, "create-image") {
		t.Errorf("expected create-image call: %s", got)
	}
	if !strings.Contains(got, "--instance-id i-deadbeef") {
		t.Errorf("expected resolved instance id in argv: %s", got)
	}
	if !strings.Contains(got, "vmlab-image,Value=v1") {
		t.Errorf("expected vmlab-image tag: %s", got)
	}
}

func TestListSnapshotsParsesDescribeImages(t *testing.T) {
	dir := t.TempDir()
	stubAws(t, dir, `cat <<JSON
[
  {"Id":"ami-1","Name":"vmlab-smoke-v1","Date":"2026-05-01T00:00:00.000Z","State":"available","Tags":[{"Key":"vmlab-image","Value":"v1"}]},
  {"Id":"ami-foreign","Name":"x","Date":"2026-05-02T00:00:00.000Z","State":"available","Tags":[{"Key":"unrelated","Value":"y"}]}
]
JSON
exit 0`)
	withPath(t, dir)

	snaps, err := New().ListSnapshots(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Name != "v1" || snaps[0].ID != "ami-1" {
		t.Fatalf("unexpected snaps: %+v", snaps)
	}
}

func TestDeleteSnapshotDeregistersThenDeletesEBS(t *testing.T) {
	dir := t.TempDir()
	args := stubAws(t, dir, `case "$2" in
describe-images) echo '[{"Id":"ami-1","Snapshots":["snap-aaa","snap-bbb"]}]'; exit 0 ;;
deregister-image) exit 0 ;;
delete-snapshot) exit 0 ;;
esac
exit 1`)
	withPath(t, dir)

	if err := New().DeleteSnapshot(context.Background(), instance("smoke", nil), "v1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	raw, _ := os.ReadFile(args)
	got := string(raw)
	if !strings.Contains(got, "deregister-image --image-id ami-1") {
		t.Errorf("expected deregister: %s", got)
	}
	if !strings.Contains(got, "delete-snapshot --snapshot-id snap-aaa") ||
		!strings.Contains(got, "delete-snapshot --snapshot-id snap-bbb") {
		t.Errorf("expected EBS snapshot deletes for both ids: %s", got)
	}
}

func TestRestoreReturnsNotInPlaceError(t *testing.T) {
	err := New().Restore(context.Background(), instance("smoke", nil), "v1")
	if err == nil || !strings.Contains(err.Error(), "ec2.imageId") {
		t.Fatalf("expected ec2.imageId hint, got: %v", err)
	}
}
