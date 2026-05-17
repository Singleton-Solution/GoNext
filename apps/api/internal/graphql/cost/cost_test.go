package cost_test

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/graphql/cost"
)

// parseOp is a tiny helper that parses a single-operation document
// and returns the OperationDefinition. Tests don't need the full
// document; cost.Analyze only walks the selection set.
func parseOp(t *testing.T, src string) *ast.OperationDefinition {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Name: "test.graphql", Input: src})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Operations) != 1 {
		t.Fatalf("want 1 operation, got %d", len(doc.Operations))
	}
	return doc.Operations[0]
}

// TestSimpleQueryUnderBudget: a flat scalar query is well under any
// realistic budget.
func TestSimpleQueryUnderBudget(t *testing.T) {
	t.Parallel()
	op := parseOp(t, `query { viewer { id handle email } }`)
	got, err := cost.Analyze(op, nil, cost.DefaultAnonymousBudget)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got >= cost.DefaultAnonymousBudget {
		t.Errorf("cost %d unexpectedly >= budget %d", got, cost.DefaultAnonymousBudget)
	}
}

// TestPathologicalDeepQueryRejected: a deeply nested query with list
// fanout at each level (the canonical N+1 abuse pattern) blows the
// anonymous budget. We use a small Posts(first: 50) selection nested
// inside itself — even three levels of that is wildly expensive
// under the multiplicative cost model.
func TestPathologicalDeepQueryRejected(t *testing.T) {
	t.Parallel()
	// posts(first:50) > edges > node > author > ... repeated.
	// Each posts level multiplies its children by 50; three levels
	// of that gives 50*50*50 = 125k against a 1000 budget.
	src := `query {
	  posts(first: 50) {
	    edges { node {
	      author {
	        ` + nestPosts(3) + `
	      }
	    } }
	  }
	}`
	op := parseOp(t, src)
	_, err := cost.Analyze(op, nil, cost.DefaultAnonymousBudget)
	if err == nil {
		t.Fatalf("expected budget-exceeded error, got none")
	}
	if !errors.Is(err, cost.ErrBudgetExceeded) {
		t.Errorf("expected ErrBudgetExceeded, got %T: %v", err, err)
	}
	var be *cost.BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BudgetError, got %T", err)
	}
	if be.Cost <= be.Budget {
		t.Errorf("BudgetError.Cost (%d) must exceed Budget (%d)", be.Cost, be.Budget)
	}
}

// TestListMultiplierClamped: even when the client requests
// first: 1_000_000, the multiplier is clamped to MaxListMultiplier.
// We verify by computing two queries and checking the cost stops
// growing past the clamp.
func TestListMultiplierClamped(t *testing.T) {
	t.Parallel()
	op1 := parseOp(t, `query { posts(first: 1000000) { edges { cursor } } }`)
	op2 := parseOp(t, `query { posts(first: `+itoa(cost.MaxListMultiplier+1000)+`) { edges { cursor } } }`)

	c1, _ := cost.Analyze(op1, nil, 1<<30)
	c2, _ := cost.Analyze(op2, nil, 1<<30)
	if c1 != c2 {
		t.Errorf("clamp not applied: cost differs between two over-cap requests (%d vs %d)", c1, c2)
	}
}

// TestResolveDefaultsZero: zero Config values resolve to the
// documented defaults.
func TestResolveDefaultsZero(t *testing.T) {
	t.Parallel()
	a, b := cost.Config{}.Resolve()
	if a != cost.DefaultAnonymousBudget {
		t.Errorf("anon: got %d, want %d", a, cost.DefaultAnonymousBudget)
	}
	if b != cost.DefaultAuthenticatedBudget {
		t.Errorf("auth: got %d, want %d", b, cost.DefaultAuthenticatedBudget)
	}
}

// TestResolveOverride: non-zero Config values override the defaults.
func TestResolveOverride(t *testing.T) {
	t.Parallel()
	a, b := cost.Config{AnonymousBudget: 42, AuthenticatedBudget: 99}.Resolve()
	if a != 42 || b != 99 {
		t.Errorf("override not applied: got %d/%d", a, b)
	}
}

// TestEmptyOperationZero: nil operation returns zero cost.
func TestEmptyOperationZero(t *testing.T) {
	t.Parallel()
	got, err := cost.Analyze(nil, nil, 100)
	if err != nil || got != 0 {
		t.Fatalf("nil op: got cost=%d err=%v, want 0/nil", got, err)
	}
}

// nest builds a nested field selection of the given depth. Each
// level repeats the same field name so the parser doesn't reject it.
func nest(depth int, field, leaf string) string {
	out := leaf
	for i := 0; i < depth; i++ {
		out = field + " { " + out + " }"
	}
	return out
}

// nestPosts builds a `posts -> edges -> node -> ...` chain to a
// given depth. This exercises the list-multiplier path of the cost
// analyzer in the test for pathological queries.
func nestPosts(levels int) string {
	if levels == 0 {
		return "posts(first: 50) { edges { node { id } } }"
	}
	return "posts(first: 50) { edges { node { author { " + nestPosts(levels-1) + " } } } }"
}

// itoa is the tiny string-builder helper, duplicated to keep the
// package import-free.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
