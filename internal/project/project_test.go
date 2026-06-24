package project

import (
	"path/filepath"
	"testing"
)

func TestDetect_LongestPathWins(t *testing.T) {
	profiles := []Profile{
		{Name: "outer", Path: "/repos/app", Target: "t1", Flow: "f1"},
		{Name: "inner", Path: "/repos/app/sub", Target: "t2", Flow: "f2"},
		{Name: "other", Path: "/elsewhere", Target: "t3", Flow: "f3"},
	}

	got, ok := Detect(profiles, "/repos/app/sub/pkg")
	if !ok || got.Name != "inner" {
		t.Fatalf("nested dir should pick deepest match; got %q ok=%v", got.Name, ok)
	}

	got, ok = Detect(profiles, "/repos/app/other")
	if !ok || got.Name != "outer" {
		t.Fatalf("dir under outer only should pick outer; got %q ok=%v", got.Name, ok)
	}

	if _, ok := Detect(profiles, "/somewhere/else"); ok {
		t.Fatalf("unmatched dir should not resolve")
	}
}

func TestDetect_ExactRootMatches(t *testing.T) {
	profiles := []Profile{{Name: "app", Path: "/repos/app", Target: "t", Flow: "f"}}
	if got, ok := Detect(profiles, "/repos/app"); !ok || got.Name != "app" {
		t.Fatalf("exact root should match; got %q ok=%v", got.Name, ok)
	}
	// A sibling that merely shares a prefix must NOT match (guards against
	// naive strings.HasPrefix without a separator boundary).
	if _, ok := Detect(profiles, "/repos/app-extra"); ok {
		t.Fatalf("prefix-only sibling must not match")
	}
}

func TestByName(t *testing.T) {
	profiles := []Profile{{Name: "a"}, {Name: "b"}}
	if _, ok := ByName(profiles, "b"); !ok {
		t.Fatalf("ByName should find b")
	}
	if _, ok := ByName(profiles, "z"); ok {
		t.Fatalf("ByName should not find z")
	}
}

func TestExpandHome(t *testing.T) {
	pr := Profile{Path: "~/Projects/x", Flow: "~/f.yaml"}
	if filepath.IsAbs(pr.ExpandedPath()) != true || pr.ExpandedPath() == pr.Path {
		t.Fatalf("ExpandedPath should resolve ~: got %q", pr.ExpandedPath())
	}
	if pr.ExpandedFlow() == pr.Flow {
		t.Fatalf("ExpandedFlow should resolve ~: got %q", pr.ExpandedFlow())
	}
}
