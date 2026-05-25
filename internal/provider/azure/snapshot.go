package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/edihasaj/vmlab/internal/provider"
)

// Snapshot creates an Azure managed image from the VM. Pattern:
//
//  1. Deallocate (`az vm deallocate`) — managed image creation requires
//     the source VM to be in the Deallocated state and generalized. We
//     skip generalization by default (it's destructive) and instead use
//     a snapshot of the OS disk (`az snapshot create`) which works on a
//     running VM. Operator can flip azure.snapshotMode=image to opt
//     into the full managed-image path.
//
// Both forms tag the resulting resource with vmlab-image=<name> +
// vmlab-source=<vm> so the rest of the Snapshotter surface (List/Delete)
// can find them without needing a separate registry.
func (p *Provider) Snapshot(ctx context.Context, i provider.Instance, name, description string) error {
	if name == "" {
		return fmt.Errorf("azure snapshot: image name required")
	}
	vm := vmName(i)
	group := rg(i)
	if vm == "" || group == "" {
		return fmt.Errorf("azure snapshot: azure.vmName and azure.resourceGroup required")
	}
	resName := "vmlab-" + i.Name + "-" + name
	tags := fmt.Sprintf("vmlab-image=%s vmlab-source=%s", name, i.Name)

	if strings.EqualFold(i.SettingString("azure", "snapshotMode"), "image") {
		// Full managed-image path. Requires the VM to be deallocated +
		// generalized; we deallocate but leave generalize to the operator
		// (it strips host identity, irreversible).
		_, err := p.run(ctx, i, "vm", "deallocate", "-g", group, "-n", vm, "--no-wait")
		if err != nil {
			return err
		}
		_, err = p.run(ctx, i, "image", "create",
			"-g", group,
			"-n", resName,
			"--source", vm,
			"--tags", tags,
		)
		return err
	}

	// Default: OS-disk snapshot — non-disruptive, works on a running VM.
	osDiskID, err := p.osDiskID(ctx, i)
	if err != nil {
		return err
	}
	_, err = p.run(ctx, i, "snapshot", "create",
		"-g", group,
		"-n", resName,
		"--source", osDiskID,
		"--tags", tags,
	)
	if err != nil {
		return err
	}
	_ = description // azure attaches descriptions via tags; we encode it as vmlab-description if set
	if description != "" {
		_, _ = p.run(ctx, i, "snapshot", "update",
			"-g", group,
			"-n", resName,
			"--set", "tags.vmlab-description="+description,
		)
	}
	return nil
}

// Restore matches AWS: not in-place. Spin a new VM whose OS disk attaches
// the named snapshot. Operator workflow: set azure.osDiskId=<id> on the
// instance YAML and `vmlab up`.
func (p *Provider) Restore(_ context.Context, _ provider.Instance, _ string) error {
	return fmt.Errorf("azure: restore by setting `azure.osDiskId: <snapshot-or-image-id>` on a fresh instance and running vmlab up")
}

// ListSnapshots returns both image-mode and snapshot-mode results so a
// caller's UI doesn't care which mode produced the entry. Tagged with
// vmlab-image=*.
func (p *Provider) ListSnapshots(ctx context.Context, i provider.Instance) ([]provider.Snapshot, error) {
	group := rg(i)
	out := make([]provider.Snapshot, 0)

	// Snapshots first.
	raw, err := p.run(ctx, i, "snapshot", "list", "-g", group, "--query",
		"[?tags.\"vmlab-image\"!=null].{Id:id,Name:name,Date:timeCreated,Tags:tags,State:provisioningState}",
		"-o", "json")
	if err == nil {
		out = append(out, parseAzureSnapList(raw, "snapshot")...)
	}

	// Then images (managed-image path).
	raw, err = p.run(ctx, i, "image", "list", "-g", group, "--query",
		"[?tags.\"vmlab-image\"!=null].{Id:id,Name:name,Date:tags.\"vmlab-created\",Tags:tags,State:provisioningState}",
		"-o", "json")
	if err == nil {
		out = append(out, parseAzureSnapList(raw, "image")...)
	}
	return out, nil
}

// DeleteSnapshot removes every resource (snapshot or image) carrying the
// matching label. Idempotent if no matches.
func (p *Provider) DeleteSnapshot(ctx context.Context, i provider.Instance, name string) error {
	group := rg(i)
	// Snapshots
	raw, err := p.run(ctx, i, "snapshot", "list", "-g", group, "--query",
		"[?tags.\"vmlab-image\"=='"+name+"'].name", "-o", "json")
	if err == nil {
		for _, n := range parseAzureNameList(raw) {
			if _, err := p.run(ctx, i, "snapshot", "delete", "-g", group, "-n", n); err != nil {
				return err
			}
		}
	}
	// Images
	raw, err = p.run(ctx, i, "image", "list", "-g", group, "--query",
		"[?tags.\"vmlab-image\"=='"+name+"'].name", "-o", "json")
	if err == nil {
		for _, n := range parseAzureNameList(raw) {
			if _, err := p.run(ctx, i, "image", "delete", "-g", group, "-n", n); err != nil {
				return err
			}
		}
	}
	return nil
}

// osDiskID resolves the source VM's managed OS-disk id, the input
// `az snapshot create --source` expects.
func (p *Provider) osDiskID(ctx context.Context, i provider.Instance) (string, error) {
	out, err := p.run(ctx, i, "vm", "show", "-g", rg(i), "-n", vmName(i), "-d",
		"--query", "storageProfile.osDisk.managedDisk.id", "-o", "tsv")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("azure: vm %s has no managed OS disk", vmName(i))
	}
	return id, nil
}

// parseAzureSnapList decodes the `--query` projection used by ListSnapshots.
// Returns provider.Snapshot rows with the vmlab-image label as Name.
func parseAzureSnapList(raw, state string) []provider.Snapshot {
	var list []struct {
		Id, Name, Date, State string
		Tags                  map[string]string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &list); err != nil {
		return nil
	}
	out := make([]provider.Snapshot, 0, len(list))
	for _, e := range list {
		label := e.Tags["vmlab-image"]
		if label == "" {
			continue
		}
		display := e.State
		if display == "" {
			display = state
		}
		out = append(out, provider.Snapshot{
			ID:    e.Id,
			Name:  label,
			Date:  e.Date,
			State: display,
		})
	}
	return out
}

// parseAzureNameList decodes a JSON list of strings (output of a tsv-ish
// query that we kept as JSON for parseability).
func parseAzureNameList(raw string) []string {
	var names []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &names); err != nil {
		return nil
	}
	return names
}
