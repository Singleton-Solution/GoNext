package hooks

import (
	"errors"
	"fmt"
	"sort"
)

// ErrCycle is returned when before/after constraints form a cycle that
// has no valid topological ordering. Detected at registration time so
// the plugin author hears about it before the first dispatch — a cycle
// surfaced only on Do would be a brutal debugging experience.
//
// errors.Is(err, ErrCycle) is true for the typed cycleError returned by
// the bus; the typed error carries the chain of subscriber Sources that
// participated in the cycle for diagnostic messages.
var ErrCycle = errors.New("hooks: ordering constraints form a cycle")

// ErrSelfConstraint is returned when a subscriber names itself in its
// own Before or After list. This is always a typo — a hook cannot
// meaningfully run "before" or "after" its own invocation — so we reject
// it at registration time rather than silently dropping the constraint.
var ErrSelfConstraint = errors.New("hooks: subscriber names itself in before/after")

// cycleError carries the participants of a constraint cycle. Embeds
// ErrCycle so errors.Is(err, ErrCycle) holds.
type cycleError struct {
	chain []string
}

func (c *cycleError) Error() string {
	if len(c.chain) == 0 {
		return ErrCycle.Error()
	}
	return fmt.Sprintf("%s: %v", ErrCycle.Error(), c.chain)
}

func (c *cycleError) Is(target error) bool {
	return target == ErrCycle
}

// selfConstraintError names the offending Source.
type selfConstraintError struct {
	source string
}

func (s *selfConstraintError) Error() string {
	return fmt.Sprintf("%s: %q", ErrSelfConstraint.Error(), s.source)
}

func (s *selfConstraintError) Is(target error) bool {
	return target == ErrSelfConstraint
}

// topoSort takes a priority-pre-sorted chain (lower priority first, ties
// broken by regOrder) and reorders it so that any subscriber X with
// `after: [Y]` ends up after Y, and any X with `before: [Z]` ends up
// before Z.
//
// Algorithm: Kahn's. Build a dependency graph where an edge A -> B means
// "A must run before B". Then repeatedly extract roots (in-degree zero)
// — when multiple roots tie, pick the one that came earliest in the
// input chain (which is already priority+regOrder-sorted), so callers
// with no constraints see the existing priority semantics unchanged.
//
// Constraints referencing unknown Sources are silently dropped at the
// caller's discretion (see resolveConstraints). The returned slice is a
// fresh allocation; the input is not modified.
//
// Cycles produce a *cycleError wrapping ErrCycle. The participants list
// is the set of nodes still in the working set when no root can be
// extracted — that's the standard Kahn cycle witness, sufficient to
// point the user at the right plugins.
func topoSort(subs []registration) ([]registration, error) {
	n := len(subs)
	if n <= 1 {
		out := make([]registration, n)
		copy(out, subs)
		return out, nil
	}

	// Index by Source so constraints (which name a Source) can find the
	// target registrations. A Source may host multiple registrations
	// (e.g. the same plugin registers two listeners) — in that case the
	// constraint applies to all of them, which is what plugin authors
	// expect: "after foo/bar" means "after anything foo/bar registered."
	indexBySource := make(map[string][]int, n)
	for i, s := range subs {
		if s.source != "" {
			indexBySource[s.source] = append(indexBySource[s.source], i)
		}
	}

	// adj[i] = set of indices j where i must run before j (i -> j edge).
	// We dedupe via a map to avoid double-counting when both A.after(B)
	// and B.before(A) are specified — they're the same edge.
	adj := make([]map[int]struct{}, n)
	indeg := make([]int, n)
	for i := range adj {
		adj[i] = map[int]struct{}{}
	}

	addEdge := func(from, to int) {
		if from == to {
			return
		}
		if _, exists := adj[from][to]; exists {
			return
		}
		adj[from][to] = struct{}{}
		indeg[to]++
	}

	for i, s := range subs {
		// `before: [X]` on i means i must run before each target named X.
		for _, target := range s.before {
			for _, j := range indexBySource[target] {
				addEdge(i, j)
			}
		}
		// `after: [X]` on i means each target named X must run before i.
		for _, target := range s.after {
			for _, j := range indexBySource[target] {
				addEdge(j, i)
			}
		}
	}

	// Kahn's with a deterministic tie-break: when multiple nodes have
	// in-degree zero, pick the one earliest in the input order. The
	// input is already priority+regOrder sorted, so this preserves the
	// existing semantics for any pair the constraints don't pin.
	//
	// We use a simple linear scan rather than a heap because the chain
	// size is bounded by the number of subscribers on a single hook —
	// in practice tens, not thousands. The O(n^2) inner cost is dwarfed
	// by the per-handler dispatch work.
	out := make([]registration, 0, n)
	taken := make([]bool, n)
	for len(out) < n {
		pick := -1
		for i := 0; i < n; i++ {
			if taken[i] || indeg[i] != 0 {
				continue
			}
			pick = i
			break // input is pre-sorted by (priority, regOrder); first match wins.
		}
		if pick == -1 {
			// No node has in-degree zero ⇒ cycle. Collect the surviving
			// nodes' Sources for the error message.
			var chain []string
			for i, t := range taken {
				if t {
					continue
				}
				name := subs[i].source
				if name == "" {
					name = fmt.Sprintf("#%d", subs[i].token)
				}
				chain = append(chain, name)
			}
			sort.Strings(chain) // deterministic for tests
			return nil, &cycleError{chain: chain}
		}
		taken[pick] = true
		out = append(out, subs[pick])
		for j := range adj[pick] {
			indeg[j]--
		}
	}
	return out, nil
}

// validateSelfConstraints checks that a single registration does not
// name its own Source in Before or After. Called at register-time, so a
// typo surfaces immediately rather than being silently dropped.
//
// A subscriber with an empty Source can never violate this rule (the
// empty name matches nothing in the lookup table), so we skip the check
// in that case.
func validateSelfConstraints(reg registration) error {
	if reg.source == "" {
		return nil
	}
	for _, b := range reg.before {
		if b == reg.source {
			return &selfConstraintError{source: reg.source}
		}
	}
	for _, a := range reg.after {
		if a == reg.source {
			return &selfConstraintError{source: reg.source}
		}
	}
	return nil
}
