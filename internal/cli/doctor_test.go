package cli

import (
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
)

func TestRecoveryInstance(t *testing.T) {
	insts := []provider.Instance{
		{Name: "win11", Tags: []string{"windows", "parallels", "vm"}},
		{Name: "ubuntu", Tags: []string{"linux", "ubuntu", "parallels"}},
	}
	cases := []struct {
		name string
		tgt  target.Target
		want string
	}{
		{
			name: "explicit instance setting wins",
			tgt: target.Target{
				Name:      "ubuntu-ssh",
				Transport: "ssh",
				Settings:  map[string]any{"instance": "ubuntu"},
			},
			want: "ubuntu",
		},
		{
			name: "exact name match",
			tgt:  target.Target{Name: "ubuntu", Transport: "ssh"},
			want: "ubuntu",
		},
		{
			name: "ambiguous tag overlap yields nothing",
			tgt:  target.Target{Name: "x", Transport: "ssh", Tags: []string{"parallels"}},
			want: "",
		},
		{
			name: "unique tag overlap matches",
			tgt:  target.Target{Name: "ubuntu-crabbox", Transport: "crabbox", Tags: []string{"ubuntu"}},
			want: "ubuntu",
		},
		{
			name: "machine-local transports never get a hint",
			tgt:  target.Target{Name: "agent-browser", Transport: "abx", Tags: []string{"ubuntu"}},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recoveryInstance(tc.tgt, insts); got != tc.want {
				t.Fatalf("recoveryInstance() = %q, want %q", got, tc.want)
			}
		})
	}
}
