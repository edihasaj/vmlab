package proc

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Sig{
		"":        SigInt,
		"int":     SigInt,
		"INT":     SigInt,
		"SIGINT":  SigInt,
		"term":    SigTerm,
		"SIGTERM": SigTerm,
		"kill":    SigKill,
		"SIGKILL": SigKill,
		"  TERM ": SigTerm,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseUnknown(t *testing.T) {
	if _, err := Parse("HUP"); err == nil {
		t.Fatal("expected error for unknown signal")
	}
}

func TestSigString(t *testing.T) {
	for s, want := range map[Sig]string{SigInt: "INT", SigTerm: "TERM", SigKill: "KILL"} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}
