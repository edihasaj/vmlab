package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// ListOrphans implements provider.OrphanSweeper. Lists every hcloud server
// carrying a `vmlab=*` label, with no filter on whether the matching
// instance is still configured locally — the goal is "anything tagged
// vmlab is fair game for sweep."
func (p *Provider) ListOrphans(ctx context.Context) ([]provider.Orphan, error) {
	if _, err := exec.LookPath("hcloud"); err != nil {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "hcloud", "server", "list", "-l", "vmlab", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hcloud server list: %w", err)
	}
	var servers []struct {
		Name   string            `json:"name"`
		Status string            `json:"status"`
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(out, &servers); err != nil {
		return nil, fmt.Errorf("parse hcloud server list: %w", err)
	}
	orphans := make([]provider.Orphan, 0, len(servers))
	for _, s := range servers {
		label := s.Labels["vmlab"]
		if label == "" {
			continue
		}
		orphans = append(orphans, provider.Orphan{
			Name:   s.Name,
			Status: s.Status,
			Label:  "vmlab=" + label,
		})
	}
	return orphans, nil
}

// DeleteOrphan removes the named server. Idempotent on "not found".
func (p *Provider) DeleteOrphan(ctx context.Context, name string) error {
	c := exec.CommandContext(ctx, "hcloud", "server", "delete", name)
	out, err := c.CombinedOutput()
	if err != nil {
		s := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(s, "not found") {
			return nil
		}
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
