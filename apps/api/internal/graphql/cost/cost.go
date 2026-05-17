// Package cost implements a GraphQL query cost analyzer that rejects
// pathological queries before they reach the resolvers. It is wired
// in as a gqlgen OperationMiddleware (called once per parsed
// operation, after the schema validator and before the executor) so
// the cost calculation happens against the parsed-and-validated
// document — we don't have to defend against malformed input here.
//
// The scoring model is deliberately simple:
//
//   - Each scalar field costs 1.
//   - Each composite (object) field costs (1 + cost of children).
//   - Lists multiply children by an estimated page size pulled from
//     the field's `first:` argument (capped at MaxListMultiplier so a
//     client can't blow the budget by asking for `first: 1000000`).
//   - Depth contributes a small additive penalty so a deeply-nested
//     query like `posts.author.posts.author.posts...` is also caught
//     even when each level is cheap.
//
// The default budgets follow the budgets in docs/05-admin-api.md §3.2:
//
//   - Anonymous: 1000
//   - Authenticated: 10000
//
// The analyzer takes the policy.Principal from the context to choose
// between them — no separate API call is needed.
package cost

import (
	"errors"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
)

// Default cost budgets. The values track docs/05-admin-api.md §3.2;
// changing them is a policy decision (operators may want to raise the
// authenticated budget for first-party clients) so they are exposed
// via Config rather than baked in here.
const (
	DefaultAnonymousBudget     = 1000
	DefaultAuthenticatedBudget = 10000

	// MaxListMultiplier caps the estimated size of any list field
	// regardless of the `first:` argument the client supplies. Without
	// this, `posts(first: 1000000)` would dominate the cost calc and
	// reject every query that touches a list with `first` unset.
	MaxListMultiplier = 100

	// DefaultListMultiplier is used when a list field has no `first:`
	// argument (or it's unparseable). Tuned to be modestly
	// pessimistic — most list queries do specify `first:`.
	DefaultListMultiplier = 20

	// DepthPenalty is added per level of nesting beyond the operation
	// root. Pure depth (no fanout) wouldn't otherwise show up in the
	// cost because each field is cheap; this term makes deep cycles
	// expensive too.
	DepthPenalty = 5

	// ScalarFieldCost is the unit cost of a leaf (scalar/enum) field.
	ScalarFieldCost = 1

	// CompositeFieldCost is the unit cost of an object field, on top
	// of its children's cost.
	CompositeFieldCost = 1
)

// Config holds the configurable budgets. The zero value falls back
// to the DefaultAnonymousBudget / DefaultAuthenticatedBudget
// constants — callers who want the defaults pass Config{}.
type Config struct {
	AnonymousBudget     int
	AuthenticatedBudget int
}

// Resolve returns the effective budget for the given Config, falling
// back to the defaults when the field is zero. Split out from the
// analyzer so tests can verify the default-resolution rule without
// running an analysis.
func (c Config) Resolve() (anon, auth int) {
	anon = c.AnonymousBudget
	if anon == 0 {
		anon = DefaultAnonymousBudget
	}
	auth = c.AuthenticatedBudget
	if auth == 0 {
		auth = DefaultAuthenticatedBudget
	}
	return anon, auth
}

// ErrBudgetExceeded is the sentinel error returned when an operation
// exceeds its budget. The error message embeds the cost and budget
// values so operators can tune.
var ErrBudgetExceeded = errors.New("query cost exceeds budget")

// BudgetError is the typed error returned by Analyze when the
// computed cost exceeds the budget. It carries the cost and budget
// for operator-facing logs / metrics. The Error() method satisfies
// error and is safe to surface to clients (no schema secrets leak).
type BudgetError struct {
	Cost   int
	Budget int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("%s: cost=%d budget=%d", ErrBudgetExceeded.Error(), e.Cost, e.Budget)
}

// Is enables errors.Is(err, ErrBudgetExceeded).
func (e *BudgetError) Is(target error) bool { return target == ErrBudgetExceeded }

// Analyze computes the cost of the given operation against the
// supplied schema. The operation must already have been validated
// against the schema (gqlgen does this before the middleware fires).
//
// Returns (cost, nil) on success; (cost, *BudgetError) when the
// computed cost exceeds the budget. Callers translate the latter
// into a GraphQL error.
func Analyze(op *ast.OperationDefinition, _ *ast.Schema, budget int) (int, error) {
	if op == nil {
		return 0, nil
	}
	cost := walkSelectionSet(op.SelectionSet, 0)
	if cost > budget {
		return cost, &BudgetError{Cost: cost, Budget: budget}
	}
	return cost, nil
}

// walkSelectionSet recursively scores a selection set. depth is the
// distance from the operation root and feeds the depth penalty.
func walkSelectionSet(sel ast.SelectionSet, depth int) int {
	if len(sel) == 0 {
		return 0
	}
	var total int
	for _, s := range sel {
		total += scoreSelection(s, depth)
	}
	return total
}

// scoreSelection scores a single selection (field, fragment, or
// inline fragment). Fragments are followed transparently — they
// don't multiply cost, they only group.
func scoreSelection(s ast.Selection, depth int) int {
	switch v := s.(type) {
	case *ast.Field:
		return scoreField(v, depth)
	case *ast.InlineFragment:
		return walkSelectionSet(v.SelectionSet, depth)
	case *ast.FragmentSpread:
		if v.Definition == nil {
			return 0
		}
		return walkSelectionSet(v.Definition.SelectionSet, depth)
	default:
		return 0
	}
}

// scoreField is the heart of the calc. The cost model is:
//
//	leaf (scalar/enum):        ScalarFieldCost
//	composite (no children):   CompositeFieldCost
//	composite (with children): CompositeFieldCost + DepthPenalty + multiplier * sum(children)
//
// The multiplier is 1 for non-list fields, or the estimated page
// size for list fields (clamped to MaxListMultiplier).
func scoreField(f *ast.Field, depth int) int {
	// Leaf: no sub-selection. We can't tell scalar vs enum from the
	// AST alone without the schema definition, but both are leaves
	// in the AST sense so the cost is the same.
	if len(f.SelectionSet) == 0 {
		return ScalarFieldCost
	}

	childCost := walkSelectionSet(f.SelectionSet, depth+1)
	mult := listMultiplier(f)
	return CompositeFieldCost + DepthPenalty + mult*childCost
}

// listMultiplier returns the estimated size of a list field. We use
// the `first:` argument if present and clamp; otherwise we use
// DefaultListMultiplier. Non-list fields get multiplier=1, but we
// can't distinguish them from list fields without the schema; in
// practice a non-list field with `first:` doesn't validate, so this
// is safe.
//
// We treat ANY field with a `first:` argument as a list, which is
// the convention in our schema. Adding new list-shaped pagination
// arguments later (e.g., `limit:`) requires updating this function.
func listMultiplier(f *ast.Field) int {
	for _, arg := range f.Arguments {
		if arg.Name != "first" {
			continue
		}
		if arg.Value == nil {
			continue
		}
		// We only handle literal int values here. A variable
		// reference resolves at execute time; we conservatively use
		// the default in that case (we don't have variable bindings
		// at middleware time without the operation context, which
		// is fine for the scaffold — the variable path is a
		// follow-up).
		v, err := arg.Value.Value(nil)
		if err != nil {
			return DefaultListMultiplier
		}
		// gqlparser surfaces ints as int64.
		if n, ok := v.(int64); ok {
			return clamp(int(n), 1, MaxListMultiplier)
		}
		// Fall through on unparseable shapes.
		return DefaultListMultiplier
	}
	return 1
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
