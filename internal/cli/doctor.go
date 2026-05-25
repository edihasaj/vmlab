package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

type doctorRow struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
}

type doctorReport struct {
	Targets []doctorRow `json:"targets"`
	OK      bool        `json:"ok"`
}

func newDoctorCmd() *cobra.Command {
	var (
		asJSON  bool
		timeout time.Duration
	)
	c := &cobra.Command{
		Use:   "doctor [selector...]",
		Short: "Verify each transport binary is present and each target is reachable",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			r, err := target.Load(p)
			if err != nil {
				return err
			}
			reg := transport.Default()
			var ts []target.Target
			if len(args) == 0 {
				ts = r.All()
			} else {
				ts, err = target.NewSelector(args...).Resolve(r)
				if err != nil {
					return err
				}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			rows := make([]doctorRow, len(ts))
			var wg sync.WaitGroup
			for i, t := range ts {
				i, t := i, t
				wg.Add(1)
				go func() {
					defer wg.Done()
					tr, err := reg.Get(t.Transport)
					if err != nil {
						rows[i] = doctorRow{Name: t.Name, Transport: t.Transport, OK: false, Message: err.Error()}
						return
					}
					h := tr.Doctor(ctx, t)
					rows[i] = doctorRow{Name: t.Name, Transport: t.Transport, OK: h.OK, Message: h.Message}
				}()
			}
			wg.Wait()

			rep := doctorReport{Targets: rows, OK: true}
			for _, r := range rows {
				if !r.OK {
					rep.OK = false
					break
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(rep)
			}
			fmt.Fprintf(out, "%-24s %-10s %-3s %s\n", "TARGET", "TRANSPORT", "OK", "MESSAGE")
			for _, r := range rows {
				ok := "no"
				if r.OK {
					ok = "yes"
				}
				fmt.Fprintf(out, "%-24s %-10s %-3s %s\n", r.Name, r.Transport, ok, r.Message)
			}
			if !rep.OK {
				return fmt.Errorf("doctor: one or more targets unhealthy")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	c.Flags().DurationVar(&timeout, "timeout", 20*time.Second, "doctor timeout (shared across all targets)")
	return c
}
