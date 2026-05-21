package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
)

// stubProvider implements provider.Provider but NOT provider.Snapshotter.
type stubProvider struct{ name string }

func (s *stubProvider) Name() string                                  { return s.name }
func (s *stubProvider) Doctor(context.Context, provider.Instance) provider.Health { return provider.Health{OK: true} }
func (s *stubProvider) Status(context.Context, provider.Instance) (provider.State, error) {
	return provider.StateReady, nil
}
func (s *stubProvider) Up(context.Context, provider.Instance) (target.Target, provider.EnsureResult, error) {
	return target.Target{}, provider.EnsureResult{}, nil
}
func (s *stubProvider) Down(context.Context, provider.Instance, provider.Dispose) error { return nil }

// snapProvider also implements Snapshotter so we can exercise the success path.
type snapProvider struct {
	stubProvider
	restored      []string
	restoreErr    error
}

func (s *snapProvider) Snapshot(_ context.Context, _ provider.Instance, _, _ string) error {
	return nil
}
func (s *snapProvider) Restore(_ context.Context, _ provider.Instance, name string) error {
	s.restored = append(s.restored, name)
	return s.restoreErr
}
func (s *snapProvider) ListSnapshots(context.Context, provider.Instance) ([]provider.Snapshot, error) {
	return nil, nil
}
func (s *snapProvider) DeleteSnapshot(context.Context, provider.Instance, string) error {
	return nil
}

func TestRestoreSnapshotIfRequestedEmptyIsNoOp(t *testing.T) {
	pr := &stubProvider{name: "stub"}
	if err := restoreSnapshotIfRequested(context.Background(), pr, provider.Instance{Name: "x"}, ""); err != nil {
		t.Fatalf("empty name must be a no-op, got %v", err)
	}
}

func TestRestoreSnapshotIfRequestedUnsupportedProvider(t *testing.T) {
	pr := &stubProvider{name: "stub"}
	err := restoreSnapshotIfRequested(context.Background(), pr, provider.Instance{Name: "x"}, "clean")
	if err == nil {
		t.Fatal("expected an error when provider lacks Snapshotter")
	}
	if !strings.Contains(err.Error(), "does not support snapshots") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestRestoreSnapshotIfRequestedCallsRestore(t *testing.T) {
	pr := &snapProvider{stubProvider: stubProvider{name: "snap"}}
	if err := restoreSnapshotIfRequested(context.Background(), pr, provider.Instance{Name: "x"}, "clean"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(pr.restored) != 1 || pr.restored[0] != "clean" {
		t.Errorf("expected Restore(\"clean\"), got %v", pr.restored)
	}
}

func TestRestoreSnapshotIfRequestedWrapsRestoreError(t *testing.T) {
	pr := &snapProvider{stubProvider: stubProvider{name: "snap"}, restoreErr: errors.New("no such snapshot")}
	err := restoreSnapshotIfRequested(context.Background(), pr, provider.Instance{Name: "x"}, "missing")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "--from-snapshot=missing") {
		t.Errorf("error should mention the snapshot name; got %v", err)
	}
}
