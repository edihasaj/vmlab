package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeWebhook captures every JSON payload Discord receives, lets the test
// pin a response code, and exposes a counter for concurrent dispatch tests.
type fakeWebhook struct {
	mu       sync.Mutex
	bodies   []map[string]any
	respCode int
	srv      *httptest.Server
}

func newFakeWebhook(t *testing.T) *fakeWebhook {
	t.Helper()
	f := &fakeWebhook{respCode: 204}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(buf, &m)
		f.mu.Lock()
		f.bodies = append(f.bodies, m)
		code := f.respCode
		f.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(func() { f.srv.Close() })
	return f
}

func (f *fakeWebhook) URL() string { return f.srv.URL }

func (f *fakeWebhook) Calls() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.bodies))
	copy(out, f.bodies)
	return out
}

func TestDiscordRendersStartMessage(t *testing.T) {
	d := NewDiscord("https://example.invalid", "")
	msg := d.render(Event{
		Phase:    PhaseStart,
		Instance: "win11-studio",
		Provider: "parallels",
		Selector: "@win11-studio",
		Cmd:      "cmd.exe /c ver",
		RunID:    "abc123",
	})
	if !strings.Contains(msg, "▶️") || !strings.Contains(msg, "@win11-studio") {
		t.Fatalf("start message missing pieces: %q", msg)
	}
	if !strings.Contains(msg, "parallels") || !strings.Contains(msg, "cmd.exe") {
		t.Fatalf("start message missing context: %q", msg)
	}
}

func TestDiscordRendersFailureWithMentionAndTail(t *testing.T) {
	d := NewDiscord("https://example.invalid", "<@42>")
	msg := d.render(Event{
		Phase:      PhaseFailure,
		Instance:   "linux-hetz",
		Selector:   "@linux-hetz",
		Cmd:        "./run.sh",
		RunID:      "deadbeef",
		ExitCode:   1,
		UpMs:       300, RunMs: 1200, DownMs: 200,
		Err:        "exit status 1",
		StderrTail: "line1\nline2\nline3",
	})
	if !strings.Contains(msg, "<@42>") {
		t.Fatalf("missing mention: %q", msg)
	}
	if !strings.Contains(msg, "exit=1") || !strings.Contains(msg, "line3") {
		t.Fatalf("failure context missing: %q", msg)
	}
}

func TestDiscordRendersMatrixTable(t *testing.T) {
	d := NewDiscord("https://example/h", "<@123>")
	msg := d.render(Event{
		Phase:    PhaseSuccess,
		Selector: "@@app-test",
		RunID:    "20260521T120000-abc",
		Matrix: []MatrixSummaryRow{
			{Target: "mac", Provider: "parallels", Status: "pass", ExitCode: 0, DurationMs: 1200},
			{Target: "linux", Provider: "hetzner", Status: "pass", ExitCode: 0, DurationMs: 1800},
			{Target: "windows", Provider: "windows", Status: "fail", ExitCode: 7, DurationMs: 900, Error: "smoke"},
		},
	})
	for _, want := range []string{
		"**matrix**",
		"@@app-test",
		"run-id `20260521T120000-abc`",
		"<@123>",
		"TARGET",
		"STATUS",
		"mac",
		"linux",
		"windows",
		"pass",
		"fail",
		"```",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in matrix message:\n%s", want, msg)
		}
	}
}

func TestDiscordMatrixAllPassNoMention(t *testing.T) {
	d := NewDiscord("https://example/h", "<@123>")
	msg := d.render(Event{
		Phase:    PhaseSuccess,
		Selector: "@@green",
		Matrix: []MatrixSummaryRow{
			{Target: "mac", Status: "pass"},
			{Target: "linux", Status: "skip"},
		},
	})
	if strings.Contains(msg, "<@123>") {
		t.Errorf("all-pass matrix must not @-mention; got:\n%s", msg)
	}
	if !strings.HasPrefix(msg, "✅") {
		t.Errorf("expected ✅ prefix on all-pass matrix; got:\n%s", msg)
	}
}

func TestDiscordPostsAndCounts204AsSuccess(t *testing.T) {
	fw := newFakeWebhook(t)
	d := NewDiscord(fw.URL(), "")
	err := d.Notify(context.Background(), Event{Phase: PhaseStart, Instance: "x"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	calls := fw.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if _, ok := calls[0]["content"].(string); !ok {
		t.Fatalf("missing content: %+v", calls[0])
	}
}

func TestDiscordReturnsErrorOnNon2xx(t *testing.T) {
	fw := newFakeWebhook(t)
	fw.respCode = 500
	d := NewDiscord(fw.URL(), "")
	if err := d.Notify(context.Background(), Event{Phase: PhaseStart}); err == nil {
		t.Fatalf("expected error on 500")
	}
}

func TestMultiSkipsPhasesNotInAllowList(t *testing.T) {
	fw := newFakeWebhook(t)
	d := NewDiscord(fw.URL(), "")
	m := &Multi{Notifiers: []Notifier{&phasedNotifier{inner: d, phases: map[Phase]bool{PhaseFailure: true}}}}
	m.Notify(context.Background(), Event{Phase: PhaseStart})
	m.Notify(context.Background(), Event{Phase: PhaseSuccess})
	m.Notify(context.Background(), Event{Phase: PhaseFailure})
	if got := len(fw.Calls()); got != 1 {
		t.Fatalf("want 1 call, got %d", got)
	}
}

func TestMultiSwallowsErrorsAndLogsToStderr(t *testing.T) {
	fw := newFakeWebhook(t)
	fw.respCode = 500
	d := NewDiscord(fw.URL(), "")
	var buf bytes.Buffer
	m := &Multi{
		Notifiers: []Notifier{&phasedNotifier{inner: d, phases: phasesAll()}},
		Stderr:    &buf,
	}
	m.Notify(context.Background(), Event{Phase: PhaseStart})
	if !strings.Contains(buf.String(), "notify(discord)") {
		t.Fatalf("expected stderr log, got %q", buf.String())
	}
}

func TestVMLABNotifyDisablesAll(t *testing.T) {
	t.Setenv("VMLAB_NOTIFY", "0")
	fw := newFakeWebhook(t)
	d := NewDiscord(fw.URL(), "")
	m := &Multi{Notifiers: []Notifier{&phasedNotifier{inner: d, phases: phasesAll()}}}
	m.Notify(context.Background(), Event{Phase: PhaseStart})
	if got := len(fw.Calls()); got != 0 {
		t.Fatalf("expected 0 calls when VMLAB_NOTIFY=0, got %d", got)
	}
}

func TestBuildFromConfigBuildsDiscord(t *testing.T) {
	m, err := Build(context.Background(), FileConfig{
		Discord: &DiscordConfig{Webhook: "https://example.invalid/hook", On: []string{"success", "failure"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(m.Notifiers) != 1 {
		t.Fatalf("want 1 notifier, got %d", len(m.Notifiers))
	}
	ph := m.Notifiers[0].Phases()
	if ph[PhaseStart] || !ph[PhaseSuccess] || !ph[PhaseFailure] {
		t.Fatalf("phases not honoured: %+v", ph)
	}
}

func TestBuildEmptyIsNoOp(t *testing.T) {
	m, err := Build(context.Background(), FileConfig{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(m.Notifiers) != 0 {
		t.Fatalf("want 0 notifiers, got %d", len(m.Notifiers))
	}
	m.Notify(context.Background(), Event{Phase: PhaseStart})
}
