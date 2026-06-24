package transport

import (
	"reflect"
	"testing"

	"github.com/edihasaj/vmlab/internal/target"
)

func TestWrapShell(t *testing.T) {
	cases := []struct {
		name string
		tgt  target.Target
		want []string
	}{
		{
			name: "linux ssh target uses posix sh",
			tgt:  target.Target{Name: "u", Transport: "ssh"},
			want: []string{"sh", "-lc", "echo hi"},
		},
		{
			name: "parallels-guest defaults to cmd.exe (was hardcoded sh -lc — broke prlctl exec on Windows guests)",
			tgt:  target.Target{Name: "w", Transport: "parallels-guest"},
			want: []string{"cmd.exe", "/c", "echo hi"},
		},
		{
			name: "ssh-windows with ssh.shell=pwsh uses pwsh.exe (PowerShell 7+)",
			tgt: target.Target{
				Name:      "w",
				Transport: "ssh-windows",
				Settings:  map[string]any{"ssh": map[string]any{"shell": "pwsh"}},
			},
			want: []string{"pwsh.exe", "-NoProfile", "-Command", "echo hi"},
		},
		{
			name: "ssh-windows with ssh.shell=powershell uses powershell.exe (5.1)",
			tgt: target.Target{
				Name:      "w",
				Transport: "ssh-windows",
				Settings:  map[string]any{"ssh": map[string]any{"shell": "powershell"}},
			},
			want: []string{"powershell.exe", "-NoProfile", "-Command", "echo hi"},
		},
		{
			name: "explicit os override flips a non-windows transport to cmd.exe",
			tgt: target.Target{
				Name:      "mixed",
				Transport: "ssh",
				Settings:  map[string]any{"os": "windows"},
			},
			want: []string{"cmd.exe", "/c", "echo hi"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WrapShell(tc.tgt, "echo hi")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("WrapShell mismatch\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}
