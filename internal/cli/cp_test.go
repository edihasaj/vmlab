package cli

import (
	"context"
	"encoding/base64"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
)

// recordingTransport captures the argv of every Run call so cp's guest-side
// command sequence can be asserted without a real VM.
type recordingTransport struct{ calls [][]string }

func (r *recordingTransport) Name() string { return "recording" }
func (r *recordingTransport) Capabilities() transport.Caps {
	return transport.Caps{}
}
func (r *recordingTransport) Doctor(context.Context, target.Target) transport.Health {
	return transport.Health{OK: true}
}
func (r *recordingTransport) Sync(context.Context, target.Target, string) error { return nil }
func (r *recordingTransport) Run(_ context.Context, _ target.Target, cmd []string, _, _ io.Writer) (transport.Result, error) {
	r.calls = append(r.calls, cmd)
	return transport.Result{ExitCode: 0}, nil
}
func (r *recordingTransport) Shell(context.Context, target.Target) error              { return nil }
func (r *recordingTransport) Screenshot(context.Context, target.Target, string) error { return nil }
func (r *recordingTransport) GUI(context.Context, target.Target, transport.GUIAction, io.Writer, io.Writer) error {
	return nil
}

var winChunkValue = regexp.MustCompile(`-Value '([^']*)' -NoNewline`)

// TestPushFileToGuestWindows verifies the Windows path streams the file as
// base64 (Set-Content for the first chunk) and reconstructs it with a
// WriteAllBytes decode — and that the streamed base64 round-trips to the
// original bytes.
func TestPushFileToGuestWindows(t *testing.T) {
	rec := &recordingTransport{}
	tgt := target.Target{Name: "win", Settings: map[string]any{"os": "windows"}}
	data := []byte("line1\nline2\twith tabs and 'quotes' and C:\\paths\\here")

	if err := pushFileToGuest(context.Background(), rec, tgt, data, "C:/dest.txt"); err != nil {
		t.Fatalf("pushFileToGuest: %v", err)
	}
	if len(rec.calls) < 2 {
		t.Fatalf("expected at least 2 guest calls, got %d", len(rec.calls))
	}

	// First call writes the first base64 chunk via Set-Content (truncating).
	first := strings.Join(rec.calls[0], " ")
	if !strings.Contains(first, "Set-Content") || strings.Contains(first, "Add-Content") {
		t.Errorf("first chunk should truncate via Set-Content, got: %s", first)
	}

	// Reassemble the base64 from every chunk call and decode it.
	var b64 strings.Builder
	for _, c := range rec.calls[:len(rec.calls)-1] {
		m := winChunkValue.FindStringSubmatch(c[len(c)-1])
		if m == nil {
			t.Fatalf("chunk call missing -Value: %v", c)
		}
		b64.WriteString(m[1])
	}
	decoded, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		t.Fatalf("reassembled base64 did not decode: %v", err)
	}
	if string(decoded) != string(data) {
		t.Errorf("round-trip mismatch:\n got %q\nwant %q", decoded, data)
	}

	// Last call decodes into the destination and cleans up the temp.
	last := rec.calls[len(rec.calls)-1][len(rec.calls[len(rec.calls)-1])-1]
	if !strings.Contains(last, "WriteAllBytes('C:/dest.txt'") || !strings.Contains(last, "Remove-Item") {
		t.Errorf("final call should WriteAllBytes to dest and remove temp, got: %s", last)
	}
}

// TestPushFileToGuestPosix verifies the posix path uses printf+base64 and a
// final base64 -d decode.
func TestPushFileToGuestPosix(t *testing.T) {
	rec := &recordingTransport{}
	tgt := target.Target{Name: "lin", Settings: map[string]any{"os": "linux"}}
	data := []byte("hello posix")

	if err := pushFileToGuest(context.Background(), rec, tgt, data, "/tmp/dest"); err != nil {
		t.Fatalf("pushFileToGuest: %v", err)
	}
	first := strings.Join(rec.calls[0], " ")
	if !strings.Contains(first, "printf %s") || !strings.Contains(first, "> '/tmp/dest.vmlabcp'") {
		t.Errorf("first posix chunk should printf into temp with truncate, got: %s", first)
	}
	last := strings.Join(rec.calls[len(rec.calls)-1], " ")
	if !strings.Contains(last, "base64 -d '/tmp/dest.vmlabcp' > '/tmp/dest'") {
		t.Errorf("final posix call should base64-decode into dest, got: %s", last)
	}
}
