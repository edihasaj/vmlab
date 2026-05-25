package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// Snapshot calls `gcloud compute machine-images create` against the
// instance. Machine images capture the VM's disks + metadata + machine
// type in one resource, so the restore-on-fresh-instance flow is simpler
// than AWS/Azure: a new instance can use `--source-machine-image`.
//
// Labels: vmlab-image=<name>, vmlab-source=<i.Name>. gcloud's label CLI
// accepts only lowercase / hyphens / digits — we leave it to the operator
// to keep names compatible (vmlab generates lowercase ids by default).
func (p *Provider) Snapshot(ctx context.Context, i provider.Instance, name, description string) error {
	if name == "" {
		return fmt.Errorf("gcp snapshot: image name required")
	}
	src := instName(i)
	if src == "" {
		return fmt.Errorf("gcp snapshot: instance name required")
	}
	resName := "vmlab-" + i.Name + "-" + name
	desc := description
	if desc == "" {
		desc = "vmlab snapshot: " + name
	}
	// gcp.run() prepends --project / --account so we don't pass them here.
	args := []string{
		"compute", "machine-images", "create", resName,
		"--source-instance", src,
		"--source-instance-zone", zone(i),
		"--description", desc,
		"--labels", "vmlab-image=" + name + ",vmlab-source=" + i.Name,
		"--format=json",
	}
	_, err := p.run(ctx, i, args...)
	return err
}

// Restore: like AWS/Azure, not in-place. The supported path is a fresh
// instance YAML pointing at the image (gcp.sourceMachineImage=<name>),
// then `vmlab up`.
func (p *Provider) Restore(_ context.Context, _ provider.Instance, _ string) error {
	return fmt.Errorf("gcp: restore by setting `gcp.sourceMachineImage: <name>` on a fresh instance and running vmlab up")
}

// ListSnapshots returns every machine image whose label set carries
// vmlab-image. Scoped to the configured project.
func (p *Provider) ListSnapshots(ctx context.Context, i provider.Instance) ([]provider.Snapshot, error) {
	args := []string{
		"compute", "machine-images", "list",
		"--filter", "labels.vmlab-image:*",
		"--format=json",
	}
	out, err := p.run(ctx, i, args...)
	if err != nil {
		return nil, err
	}
	var images []struct {
		Name              string            `json:"name"`
		CreationTimestamp string            `json:"creationTimestamp"`
		Status            string            `json:"status"`
		Labels            map[string]string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &images); err != nil {
		return nil, fmt.Errorf("parse gcloud machine-images list: %w", err)
	}
	snaps := make([]provider.Snapshot, 0, len(images))
	for _, im := range images {
		label := im.Labels["vmlab-image"]
		if label == "" {
			continue
		}
		snaps = append(snaps, provider.Snapshot{
			ID:    im.Name,
			Name:  label,
			Date:  im.CreationTimestamp,
			State: im.Status,
		})
	}
	return snaps, nil
}

// DeleteSnapshot removes machine images carrying the matching label.
// Idempotent if no matches; preserves images that share the source
// label but not the requested image label.
func (p *Provider) DeleteSnapshot(ctx context.Context, i provider.Instance, name string) error {
	listArgs := []string{
		"compute", "machine-images", "list",
		"--filter", "labels.vmlab-image=" + name,
		"--format=value(name)",
	}
	out, err := p.run(ctx, i, listArgs...)
	if err != nil {
		return err
	}
	for _, n := range strings.Fields(strings.TrimSpace(out)) {
		if _, err := p.run(ctx, i, "compute", "machine-images", "delete", n, "--quiet"); err != nil {
			return err
		}
	}
	return nil
}
