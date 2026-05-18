package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// ListOrphans returns every Azure VM tagged vmlab=*. Scope is the resource
// group set on a probe instance (callers pass an empty Instance — we use
// `vm list` with --query to filter by tag across the whole subscription).
func (p *Provider) ListOrphans(ctx context.Context) ([]provider.Orphan, error) {
	if _, err := exec.LookPath("az"); err != nil {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, "az", "vm", "list",
		"--query", "[?tags.vmlab != null].{name:name,rg:resourceGroup,tag:tags.vmlab,state:powerState}",
		"-d", "-o", "json").CombinedOutput()
	if err != nil {
		s := strings.ToLower(string(out))
		if strings.Contains(s, "not logged in") || strings.Contains(s, "please run") {
			return nil, nil
		}
		return nil, fmt.Errorf("az vm list: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var rows []struct {
		Name, Rg, Tag, State string
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse az vm list: %w", err)
	}
	orphans := make([]provider.Orphan, 0, len(rows))
	for _, r := range rows {
		// Name encodes <rg>/<vm> so DeleteOrphan can locate it.
		orphans = append(orphans, provider.Orphan{
			Name:   r.Rg + "/" + r.Name,
			Status: strings.TrimPrefix(r.State, "VM "),
			Label:  "vmlab=" + r.Tag,
		})
	}
	return orphans, nil
}

// DeleteOrphan accepts the <rg>/<vm> encoding emitted by ListOrphans.
func (p *Provider) DeleteOrphan(ctx context.Context, name string) error {
	rg, vm, ok := strings.Cut(name, "/")
	if !ok {
		return fmt.Errorf("azure orphan name must be <rg>/<vm>: %q", name)
	}
	out, err := exec.CommandContext(ctx, "az", "vm", "delete",
		"-g", rg, "-n", vm, "--yes", "--no-wait").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
