package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// Snapshot calls `hcloud server create-image --type=snapshot` against the
// instance's server, labelling the image with vmlab-image=<name> so future
// lookups can find it.
func (p *Provider) Snapshot(ctx context.Context, i provider.Instance, name, description string) error {
	srv := serverName(i)
	if srv == "" {
		return fmt.Errorf("hetzner: server name required")
	}
	if name == "" {
		return fmt.Errorf("hetzner: image name required")
	}
	desc := description
	if desc == "" {
		desc = "vmlab snapshot: " + name
	}
	args := []string{"server", "create-image", srv,
		"--type", "snapshot",
		"--description", desc,
		"--label", "vmlab-image=" + name,
		"--label", "vmlab-source=" + i.Name,
	}
	_, err := p.run(ctx, i, args...)
	return err
}

// Restore is not directly supported — Hetzner restores by spinning a new
// server FROM the saved image. Callers should set hetzner.image=<image-id>
// on a fresh instance YAML instead.
func (p *Provider) Restore(_ context.Context, _ provider.Instance, _ string) error {
	return fmt.Errorf("hetzner: restore by setting `hetzner.image: <image-id>` on a fresh instance")
}

// ListSnapshots returns every image tagged vmlab-image=*.
func (p *Provider) ListSnapshots(ctx context.Context, i provider.Instance) ([]provider.Snapshot, error) {
	out, err := p.run(ctx, i, "image", "list", "-l", "vmlab-image", "-o", "json")
	if err != nil {
		return nil, err
	}
	var images []struct {
		ID          int               `json:"id"`
		Name        string            `json:"name"`
		Created     string            `json:"created"`
		Description string            `json:"description"`
		Labels      map[string]string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &images); err != nil {
		return nil, fmt.Errorf("parse hcloud image list: %w", err)
	}
	snaps := make([]provider.Snapshot, 0, len(images))
	for _, im := range images {
		label := im.Labels["vmlab-image"]
		if label == "" {
			continue
		}
		snaps = append(snaps, provider.Snapshot{
			ID:    fmt.Sprintf("%d", im.ID),
			Name:  label,
			Date:  im.Created,
			State: im.Description,
		})
	}
	return snaps, nil
}

// DeleteSnapshot deletes images carrying the named label. Idempotent if the
// label has zero matches.
func (p *Provider) DeleteSnapshot(ctx context.Context, i provider.Instance, name string) error {
	out, err := p.run(ctx, i, "image", "list", "-l", "vmlab-image="+name, "-o", "json")
	if err != nil {
		return err
	}
	var images []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &images); err != nil {
		return fmt.Errorf("parse image list: %w", err)
	}
	for _, im := range images {
		if _, err := p.run(ctx, i, "image", "delete", fmt.Sprintf("%d", im.ID)); err != nil {
			return err
		}
	}
	return nil
}
