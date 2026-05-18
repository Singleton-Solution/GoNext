package depends

import (
	"sort"
)

// TopologicalSort returns the slugs in pending in an order that
// respects their dependency edges: a plugin appears after every
// dependency it requires.
//
// pending is the set of slugs being considered for activation in this
// pass. The graph function returns, for each slug, the slugs it
// depends on. Edges that point outside pending are ignored — those
// dependencies are expected to already be Active, and the activation
// gate has already vetted them through Resolver.Check.
//
// Returns ErrCircularDependency wrapped in a *DependencyError when
// the graph restricted to pending contains a cycle. The error's
// Slugs field lists every slug participating in the cycle, sorted,
// so the operator can see the loop at a glance.
//
// The function uses Kahn's algorithm: it's O(V+E), produces stable
// output (slugs are sorted within each "ready" wave so two runs over
// the same input produce the same order), and surfaces cycles
// naturally as "leftover" nodes that never had their in-degree drop
// to zero.
//
// We use Kahn rather than DFS because the stable wave-based output
// makes assertions in tests trivial and because Kahn doesn't need a
// recursive stack that could blow up on a dependency chain of
// thousands of plugins (very unlikely, but cheap to defend against).
func TopologicalSort(pending []string, graph func(slug string) []string) ([]string, error) {
	if len(pending) == 0 {
		return nil, nil
	}
	// Normalise pending into a set so cross-graph edges can be
	// quickly classified as "in this batch" vs "already-Active".
	want := make(map[string]struct{}, len(pending))
	for _, s := range pending {
		want[s] = struct{}{}
	}

	// in[slug] is the number of edges pointing INTO slug from another
	// pending slug. out[slug] is the slice of pending slugs that
	// depend on slug — we follow these when slug becomes ready.
	in := make(map[string]int, len(pending))
	out := make(map[string][]string, len(pending))
	for s := range want {
		in[s] = 0
	}
	for s := range want {
		for _, dep := range graph(s) {
			if _, ok := want[dep]; !ok {
				// Edge leaves the batch; ignore — already-Active
				// dependencies are not part of this sort.
				continue
			}
			in[s]++
			out[dep] = append(out[dep], s)
		}
	}

	// Seed the ready queue with every slug whose in-degree is zero.
	// Sort for deterministic output.
	ready := make([]string, 0, len(pending))
	for s, n := range in {
		if n == 0 {
			ready = append(ready, s)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(pending))
	for len(ready) > 0 {
		// Pop alphabetically smallest so the wave order is stable.
		// We resort after every batch to keep newly-ready slugs in
		// the right place; len(ready) stays small in practice
		// (manifests with hundreds of plugins are not a thing).
		s := ready[0]
		ready = ready[1:]
		order = append(order, s)
		// Slugs that depended on s lose one in-edge. Any that hit
		// zero join the ready set.
		var newly []string
		for _, dep := range out[s] {
			in[dep]--
			if in[dep] == 0 {
				newly = append(newly, dep)
			}
		}
		if len(newly) > 0 {
			ready = append(ready, newly...)
			sort.Strings(ready)
		}
	}

	if len(order) != len(pending) {
		// The leftover slugs are those still carrying a positive
		// in-degree — they form one or more cycles. List them
		// sorted so the error is reproducible.
		var cycle []string
		for s, n := range in {
			if n > 0 {
				cycle = append(cycle, s)
			}
		}
		sort.Strings(cycle)
		return nil, &DependencyError{
			Kind:  ErrCircularDependency,
			Slugs: cycle,
		}
	}
	return order, nil
}
