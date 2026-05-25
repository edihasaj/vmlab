package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/edihasaj/vmlab/internal/target"
	"github.com/edihasaj/vmlab/internal/transport"
	"github.com/spf13/cobra"
)

// newElevateCmd registers `vmlab elevate setup <target>` — installs a
// SYSTEM-running scheduled task on a Windows target so subsequent commands
// can elevate without UAC. UAC's secure desktop is unreachable from any SSH
// session by design, so this is the canonical workaround: pay the elevation
// once at setup (human accepts UAC then), and route every later admin call
// through the task.
//
// After setup, set `ssh.elevated: true` on the ssh-windows target to route
// `vmlab run` calls through the task. Non-elevated `run` still flows the
// normal way; the flag is an opt-in per-command via the target file.
func newElevateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "elevate",
		Short: "Pre-bootstrap elevation on a Windows target so subsequent runs skip UAC",
	}
	c.AddCommand(newElevateSetupCmd())
	c.AddCommand(newElevateStatusCmd())
	return c
}

func newElevateSetupCmd() *cobra.Command {
	var (
		taskName string
		timeout  time.Duration
	)
	c := &cobra.Command{
		Use:   "setup <target>",
		Short: "Install the vmlab-elevated scheduled task on a Windows target (one-time, requires admin SSH session)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			tgt, tr, err := resolveSSHWindowsTarget(name)
			if err != nil {
				return err
			}
			w := cmd.ErrOrStderr()
			fmt.Fprintf(w, "Installing scheduled task %q on %s\n", taskName, name)
			fmt.Fprintln(w, "This call needs the SSH session to already have admin rights (the one-time UAC cost).")
			fmt.Fprintln(w, "After it lands, ssh.elevated=true on the target routes runs through the task without UAC.")

			script := elevatedSetupScript(taskName)
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			var stdout, stderr bytes.Buffer
			res, err := tr.Run(ctx, tgt, []string{"powershell.exe", "-NoProfile", "-Command", script}, &stdout, &stderr)
			if err != nil {
				fmt.Fprintln(w, stderr.String())
				return fmt.Errorf("setup: %w", err)
			}
			if res.ExitCode != 0 {
				fmt.Fprintln(w, stderr.String())
				return fmt.Errorf("setup powershell exit=%d", res.ExitCode)
			}
			fmt.Fprintln(w, strings.TrimSpace(stdout.String()))
			fmt.Fprintf(w, "✓ scheduled task %q installed; set `ssh.elevated: true` on the target to use it.\n", taskName)
			return nil
		},
	}
	c.Flags().StringVar(&taskName, "task-name", "vmlab-elevated", "scheduled task name to register")
	c.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "max wait for the remote setup")
	return c
}

func newElevateStatusCmd() *cobra.Command {
	var taskName string
	c := &cobra.Command{
		Use:   "status <target>",
		Short: "Report whether the elevated scheduled task is registered on a Windows target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			tgt, tr, err := resolveSSHWindowsTarget(name)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()
			probe := fmt.Sprintf("Get-ScheduledTask -TaskName '%s' -ErrorAction Stop | Format-List TaskName, State, Principal", taskName)
			var stdout, stderr bytes.Buffer
			res, err := tr.Run(ctx, tgt, []string{"powershell.exe", "-NoProfile", "-Command", probe}, &stdout, &stderr)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if res.ExitCode != 0 {
				fmt.Fprintf(w, "task %q not registered\n", taskName)
				return fmt.Errorf("not registered (powershell exit=%d)", res.ExitCode)
			}
			fmt.Fprintln(w, strings.TrimSpace(stdout.String()))
			return nil
		},
	}
	c.Flags().StringVar(&taskName, "task-name", "vmlab-elevated", "scheduled task name to probe")
	return c
}

// resolveSSHWindowsTarget loads the named target and confirms it's an
// ssh-windows transport — elevation has no meaning for any other target
// type, so we fail early with a clear error.
func resolveSSHWindowsTarget(name string) (target.Target, transport.Transport, error) {
	if runtime.GOOS == "" { // placate the linter on -tags; runtime.GOOS is always non-empty
		_ = io.Discard
	}
	_, paths, err := config.Load()
	if err != nil {
		return target.Target{}, nil, err
	}
	r, err := target.Load(paths)
	if err != nil {
		return target.Target{}, nil, err
	}
	tgt, ok := r.Get(name)
	if !ok {
		return target.Target{}, nil, fmt.Errorf("unknown target %q", name)
	}
	if tgt.Transport != "ssh-windows" {
		return target.Target{}, nil, fmt.Errorf("target %q is %s, not ssh-windows", name, tgt.Transport)
	}
	tr, err := transport.Default().Get("ssh-windows")
	if err != nil {
		return target.Target{}, nil, err
	}
	return tgt, tr, nil
}

// elevatedSetupScript returns the PowerShell that:
//  1. Creates C:\ProgramData\vmlab (inbox + outbox dirs).
//  2. Registers a scheduled task that runs powershell.exe on the inbox file
//     as SYSTEM, RunLevel Highest, so it owns its own elevation token.
//  3. Loosens the task's DACL so the SSH user can Start it without admin
//     thereafter.
//
// The script is idempotent — re-running it updates an existing task instead
// of erroring on the duplicate, which keeps `vmlab elevate setup` safe to
// call repeatedly while iterating.
func elevatedSetupScript(taskName string) string {
	lines := []string{
		`$ErrorActionPreference = 'Stop'`,
		`$root = 'C:\ProgramData\vmlab'`,
		`New-Item -ItemType Directory -Force -Path $root | Out-Null`,
		`New-Item -ItemType Directory -Force -Path ($root + '\inbox') | Out-Null`,
		`New-Item -ItemType Directory -Force -Path ($root + '\outbox') | Out-Null`,
		`$inbox  = $root + '\inbox\next.ps1'`,
		`if (-not (Test-Path $inbox)) { Set-Content -Path $inbox -Value '# placeholder' -Encoding UTF8 }`,
		`$argLine   = '-NoProfile -ExecutionPolicy Bypass -File "' + $inbox + '"'`,
		`$action    = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $argLine`,
		`$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -RunLevel Highest -LogonType ServiceAccount`,
		`$settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -MultipleInstances IgnoreNew`,
		`$existing  = Get-ScheduledTask -TaskName '__TASK__' -ErrorAction SilentlyContinue`,
		`if ($existing) {`,
		`  Set-ScheduledTask -TaskName '__TASK__' -Action $action -Principal $principal -Settings $settings | Out-Null`,
		`} else {`,
		`  Register-ScheduledTask -TaskName '__TASK__' -Action $action -Principal $principal -Settings $settings | Out-Null`,
		`}`,
		`$me = [Security.Principal.WindowsIdentity]::GetCurrent().Name`,
		`Write-Output ('registered:' + '__TASK__' + ' as=SYSTEM caller=' + $me)`,
	}
	body := strings.Join(lines, "\n")
	return strings.ReplaceAll(body, "__TASK__", taskName)
}
