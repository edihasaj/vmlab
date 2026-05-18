package tart

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// ListOrphans returns every Tart-local VM that vmlab knows nothing about.
// Tart doesn't have arbitrary tags; we use the convention that VMs created
// via this provider start with "vmlab-" (callers can override per instance,
// but the convention is what the sweep keys off of). VMs without the
// prefix are user-managed and left alone.
func (p *Provider) ListOrphans(ctx context.Context) ([]provider.Orphan, error) {
	if _, err := exec.LookPath("tart"); err != nil {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "tart", "list", "--format=json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tart list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var rows []listEntry
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse tart list: %w", err)
	}
	orphans := make([]provider.Orphan, 0, len(rows))
	for _, r := range rows {
		if !strings.HasPrefix(r.Name, "vmlab-") {
			continue
		}
		state := r.State
		if r.Running {
			state = "running"
		}
		orphans = append(orphans, provider.Orphan{
			Name:   r.Name,
			Status: state,
			Label:  "vmlab-prefix",
		})
	}
	return orphans, nil
}

// DeleteOrphan stops (if running) then deletes the named local VM.
func (p *Provider) DeleteOrphan(ctx context.Context, name string) error {
	// Best-effort stop; ignore "not running" errors.
	_ = exec.CommandContext(ctx, "tart", "stop", name).Run()
	out, err := exec.CommandContext(ctx, "tart", "delete", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tart delete %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
