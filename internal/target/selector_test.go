package target

import (
	"reflect"
	"sort"
	"testing"
)

func mkRegistry(targets ...Target) *Registry {
	r := &Registry{byName: map[string]Target{}}
	for _, t := range targets {
		r.byName[t.Name] = t
		r.order = append(r.order, t.Name)
	}
	sort.Strings(r.order)
	return r
}

func names(ts []Target) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

func TestSelectorAll(t *testing.T) {
	r := mkRegistry(
		Target{Name: "a", Tags: []string{"linux"}},
		Target{Name: "b", Tags: []string{"mobile"}},
	)
	got, err := NewSelector("all").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"a", "b"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestSelectorTagAndExact(t *testing.T) {
	r := mkRegistry(
		Target{Name: "a", Tags: []string{"linux", "vm"}},
		Target{Name: "b", Tags: []string{"linux"}},
		Target{Name: "c", Tags: []string{"mobile"}},
	)
	got, err := NewSelector("@linux").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"a", "b"}) {
		t.Fatalf("got %v", names(got))
	}
	got, err = NewSelector("@linux,@vm").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"a"}) {
		t.Fatalf("got %v", names(got))
	}
	got, err = NewSelector("c").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"c"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestSelectorExclusion(t *testing.T) {
	r := mkRegistry(
		Target{Name: "a", Tags: []string{"linux"}},
		Target{Name: "b", Tags: []string{"linux", "ci"}},
	)
	got, err := NewSelector("@linux,not:@ci").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"a"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestSelectorUnion(t *testing.T) {
	r := mkRegistry(
		Target{Name: "a", Tags: []string{"linux"}},
		Target{Name: "b", Tags: []string{"mobile"}},
		Target{Name: "c", Tags: []string{"web"}},
	)
	got, err := NewSelector("@linux;@mobile").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"a", "b"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestSelectorMultipleArgs(t *testing.T) {
	r := mkRegistry(
		Target{Name: "a", Tags: []string{"linux"}},
		Target{Name: "b", Tags: []string{"mobile"}},
	)
	got, err := NewSelector("a", "@mobile").Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"a", "b"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestSelectorUnknownTarget(t *testing.T) {
	r := mkRegistry(Target{Name: "a"})
	if _, err := NewSelector("missing").Resolve(r); err == nil {
		t.Fatal("expected error for unknown target")
	}
}
