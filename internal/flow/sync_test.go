package flow

import (
	"bytes"
	"context"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

// Sync was previously a vmlab init template-only keyword that the flow runner
// did not actually execute (and the schema rejected). Regression: every step
// kind in the schema must have a runner branch.
func TestSyncStepCallsTransportSync(t *testing.T) {
	ft := &fakeTransport{}
	tgt := target.Target{Name: "u", Transport: "ssh"}
	f := &Flow{Steps: []Step{{Sync: "."}}}
	var out, errb bytes.Buffer
	steps, err := Run(context.Background(), ft, tgt, f, &out, &errb)
	if err != nil {
		t.Fatalf("flow.Run: %v", err)
	}
	if len(steps) != 1 || steps[0].Kind != "sync" {
		t.Fatalf("expected single sync step result, got %+v", steps)
	}
	if len(ft.syncs) != 1 || ft.syncs[0].Src != "." {
		t.Fatalf("expected one Sync call with src='.', got %+v", ft.syncs)
	}
}
