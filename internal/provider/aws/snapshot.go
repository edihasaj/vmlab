package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// Snapshot calls `aws ec2 create-image` on the running instance tagged
// vmlab=<i.Name>. The resulting AMI is tagged vmlab-image=<name> +
// vmlab-source=<i.Name> so ListSnapshots can find it later and
// DeleteSnapshot can clean it up. We default --no-reboot off (i.e. allow
// the reboot) for filesystem consistency; callers who need a hot image
// without disruption can set ec2.snapshotNoReboot=true in the instance
// settings.
//
// Note: create-image is async — AWS returns the AMI id immediately while
// the image moves through "pending" → "available". We don't poll here;
// ListSnapshots reflects whatever AWS has converged to at call time.
func (p *Provider) Snapshot(ctx context.Context, i provider.Instance, name, description string) error {
	id, _, err := p.findByTag(ctx, i)
	if err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("aws snapshot: no running instance tagged vmlab=%s", i.Name)
	}
	if name == "" {
		return fmt.Errorf("aws snapshot: image name required")
	}
	desc := description
	if desc == "" {
		desc = "vmlab snapshot: " + name
	}
	args := []string{
		"ec2", "create-image",
		"--instance-id", id,
		"--name", "vmlab-" + i.Name + "-" + name,
		"--description", desc,
		"--tag-specifications",
		fmt.Sprintf(
			"ResourceType=image,Tags=[{Key=vmlab-image,Value=%s},{Key=vmlab-source,Value=%s}]",
			name, i.Name,
		),
	}
	if i.Setting("ec2", "snapshotNoReboot") == true {
		args = append(args, "--no-reboot")
	}
	_, err = p.run(ctx, i, args...)
	return err
}

// Restore is intentionally not in-place: AWS doesn't restore a running
// instance from an AMI without terminating + recreating. The supported
// path is to set ec2.imageId=<ami-id> on a fresh instance YAML and
// `vmlab up <name>`; vmlab's existing Up flow handles it cleanly.
func (p *Provider) Restore(_ context.Context, _ provider.Instance, _ string) error {
	return fmt.Errorf("aws: restore by setting `ec2.imageId: <ami-id>` on a fresh instance and running vmlab up")
}

// ListSnapshots returns every AMI tagged vmlab-image=*, scoped to the
// caller's account via --owners self so foreign images don't leak in.
func (p *Provider) ListSnapshots(ctx context.Context, i provider.Instance) ([]provider.Snapshot, error) {
	out, err := p.run(ctx, i,
		"ec2", "describe-images",
		"--owners", "self",
		"--filters", "Name=tag-key,Values=vmlab-image",
		"--query", "Images[].{Id:ImageId,Name:Name,Date:CreationDate,State:State,Tags:Tags}",
		"--output", "json")
	if err != nil {
		return nil, err
	}
	type tag struct {
		Key, Value string
	}
	var images []struct {
		Id, Name, Date, State string
		Tags                  []tag
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &images); err != nil {
		return nil, fmt.Errorf("parse describe-images: %w", err)
	}
	snaps := make([]provider.Snapshot, 0, len(images))
	for _, im := range images {
		label := ""
		for _, t := range im.Tags {
			if t.Key == "vmlab-image" {
				label = t.Value
				break
			}
		}
		if label == "" {
			continue
		}
		snaps = append(snaps, provider.Snapshot{
			ID:    im.Id,
			Name:  label,
			Date:  im.Date,
			State: im.State,
		})
	}
	return snaps, nil
}

// DeleteSnapshot deregisters the AMI(s) matching the label, then deletes
// the backing EBS snapshots they referenced. AMIs without an explicit
// matching label are left alone so a stray vmlab-source-only image
// doesn't get nuked by a name-only delete.
func (p *Provider) DeleteSnapshot(ctx context.Context, i provider.Instance, name string) error {
	// Resolve all matching images + the EBS snapshot ids they reference.
	out, err := p.run(ctx, i,
		"ec2", "describe-images",
		"--owners", "self",
		"--filters", "Name=tag:vmlab-image,Values="+name,
		"--query", "Images[].{Id:ImageId,Snapshots:BlockDeviceMappings[].Ebs.SnapshotId}",
		"--output", "json")
	if err != nil {
		return err
	}
	var images []struct {
		Id        string
		Snapshots []string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &images); err != nil {
		return fmt.Errorf("parse describe-images: %w", err)
	}
	for _, im := range images {
		// Deregister first; only then are the backing EBS snapshots free
		// to delete (AWS rejects delete on snapshots still tied to a
		// registered AMI).
		if _, err := p.run(ctx, i, "ec2", "deregister-image", "--image-id", im.Id); err != nil {
			return err
		}
		for _, snap := range im.Snapshots {
			if snap == "" {
				continue
			}
			if _, err := p.run(ctx, i, "ec2", "delete-snapshot", "--snapshot-id", snap); err != nil {
				return err
			}
		}
	}
	return nil
}
