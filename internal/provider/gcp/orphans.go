package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// ListOrphans returns every GCE instance labelled vmlab=* in the active
// project. Cross-zone — gcloud will scan all zones unless --zones is set.
func (p *Provider) ListOrphans(ctx context.Context) ([]provider.Orphan, error) {
	if _, err := exec.LookPath("gcloud"); err != nil {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "gcloud", "compute", "instances", "list",
		"--filter=labels.vmlab:*",
		"--format=json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gcloud compute instances list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var rows []struct {
		Name   string            `json:"name"`
		Status string            `json:"status"`
		Zone   string            `json:"zone"` // full URL
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse list: %w", err)
	}
	orphans := make([]provider.Orphan, 0, len(rows))
	for _, r := range rows {
		label := r.Labels["vmlab"]
		if label == "" {
			continue
		}
		// Encode <zone>/<name> so DeleteOrphan can pass --zone.
		zone := r.Zone
		if i := strings.LastIndex(zone, "/"); i >= 0 {
			zone = zone[i+1:]
		}
		orphans = append(orphans, provider.Orphan{
			Name:   zone + "/" + r.Name,
			Status: strings.ToLower(r.Status),
			Label:  "vmlab=" + label,
		})
	}
	return orphans, nil
}

// DeleteOrphan accepts the <zone>/<name> encoding emitted by ListOrphans.
func (p *Provider) DeleteOrphan(ctx context.Context, name string) error {
	zone, vm, ok := strings.Cut(name, "/")
	if !ok {
		return fmt.Errorf("gcp orphan name must be <zone>/<vm>: %q", name)
	}
	out, err := exec.CommandContext(ctx, "gcloud", "compute", "instances", "delete", vm,
		"--zone", zone, "--quiet").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
