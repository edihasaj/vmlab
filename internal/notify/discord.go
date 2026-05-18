package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Discord posts events to a Discord webhook URL.
//
//	{"content": "<message>"}
//
// Failures (non-2xx) return an error with the response body so the caller
// can log usefully.
type Discord struct {
	Webhook           string
	MentionOnFailure  string // e.g. "<@123456789012345678>"
	HTTP              *http.Client
	UsernameOverride  string // optional: shows as the bot name
	stderrTailMaxLen  int    // 0 = default 800
	stderrTailMaxLine int    // 0 = default 6
}

// NewDiscord returns a Discord notifier with sane defaults.
func NewDiscord(webhook, mention string) *Discord {
	return &Discord{
		Webhook:          webhook,
		MentionOnFailure: mention,
		HTTP:             &http.Client{Timeout: 5 * time.Second},
	}
}

// Name implements Notifier.
func (d *Discord) Name() string { return "discord" }

// phasesAll is the shared default set, mutated only by config loading.
func phasesAll() map[Phase]bool {
	return map[Phase]bool{PhaseStart: true, PhaseSuccess: true, PhaseFailure: true}
}

// Phases implements Notifier — overridden by the Multi factory when config
// supplies an explicit `on:` list.
func (d *Discord) Phases() map[Phase]bool { return phasesAll() }

// Notify implements Notifier.
func (d *Discord) Notify(ctx context.Context, ev Event) error {
	body := map[string]any{
		"content": d.render(ev),
	}
	if d.UsernameOverride != "" {
		body["username"] = d.UsernameOverride
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Webhook, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := d.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("discord webhook: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// render builds the message body. Kept on the type so renderers can be tested
// without hitting the network.
func (d *Discord) render(ev Event) string {
	var b strings.Builder
	switch ev.Phase {
	case PhaseStart:
		b.WriteString("▶️ **start** ")
	case PhaseSuccess:
		b.WriteString("✅ **success** ")
	case PhaseFailure:
		b.WriteString("❌ **failure** ")
		if d.MentionOnFailure != "" {
			b.WriteString(d.MentionOnFailure + " ")
		}
	}
	if ev.Selector != "" {
		b.WriteString(ev.Selector)
	} else if ev.Instance != "" {
		b.WriteString("@" + ev.Instance)
	}
	if ev.Provider != "" {
		fmt.Fprintf(&b, " · `%s`", ev.Provider)
	}
	if ev.Cmd != "" {
		fmt.Fprintf(&b, " · `%s`", truncate(ev.Cmd, 80))
	}
	if ev.RunID != "" {
		fmt.Fprintf(&b, "\nrun-id: `%s`", ev.RunID)
	}
	if ev.Phase != PhaseStart {
		total := ev.TotalMs()
		if total > 0 {
			fmt.Fprintf(&b, " · up=%dms run=%dms down=%dms (total %s)",
				ev.UpMs, ev.RunMs, ev.DownMs, fmtDur(total))
		}
		if ev.ExitCode != 0 {
			fmt.Fprintf(&b, " · exit=%d", ev.ExitCode)
		}
	}
	if ev.Phase == PhaseFailure {
		if ev.Err != "" {
			fmt.Fprintf(&b, "\n```\n%s\n```", truncate(ev.Err, 400))
		}
		tail := d.tailStderr(ev.StderrTail)
		if tail != "" {
			fmt.Fprintf(&b, "\nstderr tail:\n```\n%s\n```", tail)
		}
	}
	if ev.EvidenceDir != "" {
		fmt.Fprintf(&b, "\nevidence: `%s`", ev.EvidenceDir)
	}
	return b.String()
}

func (d *Discord) tailStderr(s string) string {
	if s == "" {
		return ""
	}
	maxBytes := d.stderrTailMaxLen
	if maxBytes == 0 {
		maxBytes = 800
	}
	maxLines := d.stderrTailMaxLine
	if maxLines == 0 {
		maxLines = 6
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := strings.Join(lines, "\n")
	return truncate(out, maxBytes)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return s[:n-1] + "…"
}

func fmtDur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return d.String()
	}
	return d.Round(100 * time.Millisecond).String()
}
