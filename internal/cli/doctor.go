package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

type doctorRow struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
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

			// Per-target context so one slow probe can't bleed time from the
			// rest of the fleet. The flag remains the per-target budget
			// (renamed in --help); the overall wall-clock is bounded by the
			// caller's context (cmd.Context()).
			rows := make([]doctorRow, len(ts))
			var wg sync.WaitGroup
			for i, t := range ts {
				i, t := i, t
				wg.Add(1)
				go func() {
					defer wg.Done()
					tCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
					defer cancel()
					tr, err := reg.Get(t.Transport)
					if err != nil {
						rows[i] = doctorRow{Name: t.Name, Transport: t.Transport, OK: false, Message: err.Error()}
						return
					}
					h := tr.Doctor(tCtx, t)
					rows[i] = doctorRow{Name: t.Name, Transport: t.Transport, OK: h.OK, Message: h.Message}
				}()
			}
			wg.Wait()

			// An unreachable target often just means its backing VM is
			// suspended. Point at the instance that can revive it so an
			// agent runs `vmlab up` instead of falling back to manual
			// ssh/prlctl spelunking.
			if insts, ierr := provider.LoadInstances(p); ierr == nil {
				for i := range rows {
					if rows[i].OK {
						continue
					}
					if name := recoveryInstance(ts[i], insts.All()); name != "" {
						rows[i].Hint = fmt.Sprintf("try: vmlab up %s", name)
					}
				}
			}

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
				msg := r.Message
				if r.Hint != "" {
					msg += " — " + r.Hint
				}
				fmt.Fprintf(out, "%-24s %-10s %-3s %s\n", r.Name, r.Transport, ok, msg)
			}
			if !rep.OK {
				return fmt.Errorf("doctor: one or more targets unhealthy")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	c.Flags().DurationVar(&timeout, "timeout", 20*time.Second, "per-target doctor timeout")
	return c
}

// recoveryInstance returns the name of an instance that plausibly backs the
// given target: an explicit `instance:` setting on the target wins, then an
// exact name match, then a tag overlap — but only when exactly one instance
// overlaps, since generic tags like `parallels` span unrelated VMs. Machine-
// local transports (local, abx, guiport) never need a VM brought up.
func recoveryInstance(t target.Target, insts []provider.Instance) string {
	switch t.Transport {
	case "local", "abx", "guiport":
		return ""
	}
	if name := t.SettingString("instance"); name != "" {
		return name
	}
	for _, in := range insts {
		if in.Name == t.Name {
			return in.Name
		}
	}
	var overlapping []string
	for _, in := range insts {
		for _, tag := range in.Tags {
			if t.HasTag(tag) {
				overlapping = append(overlapping, in.Name)
				break
			}
		}
	}
	if len(overlapping) == 1 {
		return overlapping[0]
	}
	return ""
}
