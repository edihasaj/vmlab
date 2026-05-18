package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/evidence"
	"github.com/spf13/cobra"
)

// usageRow is one aggregated line for vmlab usage. Costs are derived later
// (price catalogue per provider+size); for v1 we surface raw uptime ms +
// run counts so users can pipe to their own pricing models via --json.
type usageRow struct {
	Provider string `json:"provider"`
	Instance string `json:"instance"`
	Runs     int    `json:"runs"`
	Failures int    `json:"failures"`
	UpMs     int64  `json:"upMs"`
	RunMs    int64  `json:"runMs"`
	DownMs   int64  `json:"downMs"`
	TotalMs  int64  `json:"totalMs"`
}

func newUsageCmd() *cobra.Command {
	var (
		asJSON bool
		since  time.Duration
		groupBy string
	)
	c := &cobra.Command{
		Use:   "usage",
		Short: "Summarise lifecycle uptime across recent runs",
		Long: `Walks ~/.vmlab/runs/*/meta.json and aggregates the lifecycle.upMs +
runMs + downMs window per provider × instance — the billable-uptime proxy.

  vmlab usage                           # all runs, grouped by provider+instance
  vmlab usage --since 24h               # last 24h only
  vmlab usage --group-by provider       # aggregate to provider only
  vmlab usage --json                    # pipe to jq / your cost model`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			runs, err := evidence.List(cfg.RunsDir)
			if err != nil {
				return err
			}
			cutoff := time.Time{}
			if since > 0 {
				cutoff = time.Now().Add(-since)
			}

			agg := map[string]*usageRow{}
			for _, r := range runs {
				if r.Lifecycle == nil {
					continue
				}
				if !cutoff.IsZero() && r.StartedAt.Before(cutoff) {
					continue
				}
				key := r.Lifecycle.Provider + "\x00" + r.Lifecycle.Instance
				if groupBy == "provider" {
					key = r.Lifecycle.Provider
				}
				row := agg[key]
				if row == nil {
					row = &usageRow{Provider: r.Lifecycle.Provider}
					if groupBy != "provider" {
						row.Instance = r.Lifecycle.Instance
					}
					agg[key] = row
				}
				row.Runs++
				if r.ExitCode != 0 || r.Lifecycle.Error != "" {
					row.Failures++
				}
				row.UpMs += r.Lifecycle.UpMs
				row.RunMs += r.Lifecycle.RunMs
				row.DownMs += r.Lifecycle.DownMs
				row.TotalMs += r.Lifecycle.UpMs + r.Lifecycle.RunMs + r.Lifecycle.DownMs
			}

			rows := make([]usageRow, 0, len(agg))
			for _, r := range agg {
				rows = append(rows, *r)
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].TotalMs != rows[j].TotalMs {
					return rows[i].TotalMs > rows[j].TotalMs
				}
				if rows[i].Provider != rows[j].Provider {
					return rows[i].Provider < rows[j].Provider
				}
				return rows[i].Instance < rows[j].Instance
			})

			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "no runs with lifecycle data")
				return nil
			}
			if groupBy == "provider" {
				fmt.Fprintf(out, "%-12s %-6s %-6s %-10s %-10s %-10s %s\n",
					"PROVIDER", "RUNS", "FAIL", "UP", "RUN", "DOWN", "TOTAL")
				for _, r := range rows {
					fmt.Fprintf(out, "%-12s %-6d %-6d %-10s %-10s %-10s %s\n",
						r.Provider, r.Runs, r.Failures,
						msToDur(r.UpMs), msToDur(r.RunMs), msToDur(r.DownMs), msToDur(r.TotalMs))
				}
				return nil
			}
			fmt.Fprintf(out, "%-12s %-18s %-6s %-6s %-10s %-10s %-10s %s\n",
				"PROVIDER", "INSTANCE", "RUNS", "FAIL", "UP", "RUN", "DOWN", "TOTAL")
			for _, r := range rows {
				fmt.Fprintf(out, "%-12s %-18s %-6d %-6d %-10s %-10s %-10s %s\n",
					r.Provider, r.Instance, r.Runs, r.Failures,
					msToDur(r.UpMs), msToDur(r.RunMs), msToDur(r.DownMs), msToDur(r.TotalMs))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	c.Flags().DurationVar(&since, "since", 0, "only include runs newer than this duration (e.g. 24h, 7d)")
	c.Flags().StringVar(&groupBy, "group-by", "instance", "instance | provider")
	return c
}

// msToDur renders milliseconds as a compact duration. <1s prints raw ms;
// otherwise standard time.Duration shorthand.
func msToDur(ms int64) string {
	if ms <= 0 {
		return "0"
	}
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	s := d.Round(time.Second).String()
	// Drop trailing 0m or 0s for readability.
	s = strings.TrimSuffix(s, "0s")
	return s
}
