package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// ListOrphans returns every EC2 instance tagged vmlab=* in the current
// region. AWS lacks a single "default region" concept inside the CLI,
// so the user's configured profile/region applies.
func (p *Provider) ListOrphans(ctx context.Context) ([]provider.Orphan, error) {
	if _, err := exec.LookPath("aws"); err != nil {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "aws", "ec2", "describe-instances",
		"--filters", "Name=tag-key,Values=vmlab", "Name=instance-state-name,Values=pending,running,stopped,stopping",
		"--query", "Reservations[].Instances[].{Id:InstanceId,State:State.Name,Tag:Tags[?Key=='vmlab']|[0].Value}",
		"--output", "json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("aws ec2 describe-instances: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var rows []struct {
		Id, State, Tag string
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse describe-instances: %w", err)
	}
	orphans := make([]provider.Orphan, 0, len(rows))
	for _, r := range rows {
		if r.Tag == "" {
			continue
		}
		orphans = append(orphans, provider.Orphan{
			Name:   r.Id,
			Status: r.State,
			Label:  "vmlab=" + r.Tag,
		})
	}
	return orphans, nil
}

// DeleteOrphan terminates the named instance ID.
func (p *Provider) DeleteOrphan(ctx context.Context, name string) error {
	out, err := exec.CommandContext(ctx, "aws", "ec2", "terminate-instances",
		"--instance-ids", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
