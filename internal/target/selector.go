package target

import (
	"fmt"
	"strings"
)

// Selector parses an expression and returns matching targets from a Registry.
//
// Grammar (simple, deterministic):
//
//	all                       => every target
//	<name>                    => exact target by name
//	@tag                      => any target with tag
//	@a,@b                     => AND (must have both tags)
//	not:@tag                  => exclusion (chained with previous selector)
//	a;b                       => union (a or b)
//
// Multiple top-level args (space-separated) are union.
type Selector struct{ exprs []string }

// NewSelector builds a selector from one or more arguments.
func NewSelector(args ...string) Selector {
	out := make([]string, 0, len(args))
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		out = append(out, a)
	}
	return Selector{exprs: out}
}

// Resolve returns the deduplicated list of targets matched by the selector.
func (s Selector) Resolve(r *Registry) ([]Target, error) {
	if len(s.exprs) == 0 {
		return nil, fmt.Errorf("empty selector")
	}
	seen := map[string]bool{}
	var out []Target
	for _, e := range s.exprs {
		matched, err := resolveOne(e, r)
		if err != nil {
			return nil, err
		}
		for _, t := range matched {
			if !seen[t.Name] {
				seen[t.Name] = true
				out = append(out, t)
			}
		}
	}
	return out, nil
}

func resolveOne(expr string, r *Registry) ([]Target, error) {
	// Split on `;` for unions, then iterate.
	parts := strings.Split(expr, ";")
	var out []Target
	seen := map[string]bool{}
	for _, p := range parts {
		matched, err := resolveAnd(p, r)
		if err != nil {
			return nil, err
		}
		for _, t := range matched {
			if !seen[t.Name] {
				seen[t.Name] = true
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// resolveAnd handles `,`-joined conjunctions plus `not:` exclusions.
func resolveAnd(expr string, r *Registry) ([]Target, error) {
	parts := strings.Split(expr, ",")
	if len(parts) == 0 {
		return nil, nil
	}

	// Bootstrap candidate set from the first non-exclusion atom.
	var candidates []Target
	bootstrapped := false
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "not:") {
			continue
		}
		matched, err := atomMatch(p, r)
		if err != nil {
			return nil, err
		}
		if !bootstrapped {
			candidates = matched
			bootstrapped = true
			continue
		}
		// AND: keep only targets present in both.
		set := map[string]bool{}
		for _, t := range matched {
			set[t.Name] = true
		}
		next := candidates[:0]
		for _, t := range candidates {
			if set[t.Name] {
				next = append(next, t)
			}
		}
		candidates = next
	}
	if !bootstrapped {
		return nil, fmt.Errorf("selector has only exclusions: %q", expr)
	}
	// Apply exclusions.
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !strings.HasPrefix(p, "not:") {
			continue
		}
		excl, err := atomMatch(strings.TrimPrefix(p, "not:"), r)
		if err != nil {
			return nil, err
		}
		exclSet := map[string]bool{}
		for _, t := range excl {
			exclSet[t.Name] = true
		}
		next := candidates[:0]
		for _, t := range candidates {
			if !exclSet[t.Name] {
				next = append(next, t)
			}
		}
		candidates = next
	}
	return candidates, nil
}

func atomMatch(atom string, r *Registry) ([]Target, error) {
	atom = strings.TrimSpace(atom)
	switch {
	case atom == "all":
		return r.All(), nil
	case strings.HasPrefix(atom, "@"):
		tag := strings.TrimPrefix(atom, "@")
		var out []Target
		for _, t := range r.All() {
			if t.HasTag(tag) {
				out = append(out, t)
			}
		}
		return out, nil
	default:
		t, ok := r.Get(atom)
		if !ok {
			return nil, fmt.Errorf("unknown target: %q", atom)
		}
		return []Target{t}, nil
	}
}
