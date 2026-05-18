package depends

import (
	"errors"
	"testing"
)

// graphFunc builds the function TopologicalSort expects, from a
// static map of slug → dependencies.
func graphFunc(adj map[string][]string) func(string) []string {
	return func(s string) []string { return adj[s] }
}

// TestTopologicalSort_Empty ensures the trivial input doesn't trip a
// nil-pointer or return spurious cycles.
func TestTopologicalSort_Empty(t *testing.T) {
	t.Parallel()
	order, err := TopologicalSort(nil, graphFunc(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("order: got %v want empty", order)
	}
}

// TestTopologicalSort_LinearChain pins the basic property: a → b → c
// must come back as [c, b, a].
func TestTopologicalSort_LinearChain(t *testing.T) {
	t.Parallel()
	adj := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": nil,
	}
	order, err := TopologicalSort([]string{"a", "b", "c"}, graphFunc(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 || order[0] != "c" || order[1] != "b" || order[2] != "a" {
		t.Errorf("order: got %v want [c b a]", order)
	}
}

// TestTopologicalSort_Diamond ensures a diamond dependency
// (A → B,C; B → D; C → D) resolves with D first.
func TestTopologicalSort_Diamond(t *testing.T) {
	t.Parallel()
	adj := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
		"d": nil,
	}
	order, err := TopologicalSort([]string{"a", "b", "c", "d"}, graphFunc(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("order length: got %d", len(order))
	}
	if order[0] != "d" {
		t.Errorf("d should be first; got order=%v", order)
	}
	if order[3] != "a" {
		t.Errorf("a should be last; got order=%v", order)
	}
	// b and c must appear before a and after d.
	pos := indexMap(order)
	if pos["b"] <= pos["d"] || pos["c"] <= pos["d"] {
		t.Errorf("b/c must come after d: %v", order)
	}
	if pos["a"] <= pos["b"] || pos["a"] <= pos["c"] {
		t.Errorf("a must come after b/c: %v", order)
	}
}

// TestTopologicalSort_Cycle ensures cycles surface as
// ErrCircularDependency with the participating slugs.
func TestTopologicalSort_Cycle(t *testing.T) {
	t.Parallel()
	adj := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	}
	_, err := TopologicalSort([]string{"a", "b", "c"}, graphFunc(adj))
	if err == nil {
		t.Fatal("want ErrCircularDependency, got nil")
	}
	if !errors.Is(err, ErrCircularDependency) {
		t.Fatalf("want ErrCircularDependency, got %v", err)
	}
	var de *DependencyError
	if !errors.As(err, &de) {
		t.Fatalf("want *DependencyError, got %T", err)
	}
	if len(de.Slugs) != 3 {
		t.Errorf("cycle slugs: got %v want all three", de.Slugs)
	}
}

// TestTopologicalSort_SelfCycle ensures a single-node cycle (a → a) is
// still detected.
func TestTopologicalSort_SelfCycle(t *testing.T) {
	t.Parallel()
	adj := map[string][]string{"a": {"a"}}
	_, err := TopologicalSort([]string{"a"}, graphFunc(adj))
	if !errors.Is(err, ErrCircularDependency) {
		t.Fatalf("want ErrCircularDependency, got %v", err)
	}
}

// TestTopologicalSort_EdgesOutsideBatch confirms edges to slugs not in
// the pending list are ignored: a pending plugin can depend on an
// already-Active one without affecting the sort.
func TestTopologicalSort_EdgesOutsideBatch(t *testing.T) {
	t.Parallel()
	adj := map[string][]string{
		"a": {"gn-already-active"}, // not in pending
		"b": {"a"},
	}
	order, err := TopologicalSort([]string{"a", "b"}, graphFunc(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("order: got %v want [a b]", order)
	}
}

func indexMap(order []string) map[string]int {
	out := make(map[string]int, len(order))
	for i, s := range order {
		out[s] = i
	}
	return out
}
