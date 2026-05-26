package templates_test

import (
	"errors"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/theme/templates"
)

// mapFiles is an in-memory ThemeFiles implementation used throughout
// the templates test suite. Keys are the bare basenames the resolver
// queries; values are unused. The empty map is a theme that ships no
// files at all (used to assert "parent owns" cases).
type mapFiles map[string]struct{}

func (m mapFiles) Has(name string) bool {
	_, ok := m[name]
	return ok
}

// TestResolveTemplate_ParentOnly covers the fallback path: the child
// ships nothing, the parent ships the file, so the parent index wins.
func TestResolveTemplate_ParentOnly(t *testing.T) {
	t.Parallel()
	child := mapFiles{}
	parent := mapFiles{"single.tsx": {}}
	search := templates.SearchPath{child, parent}

	idx, ok := templates.ResolveTemplate("single.tsx", search)
	if !ok {
		t.Fatalf("ResolveTemplate: expected hit on parent, got miss")
	}
	if idx != 1 {
		t.Errorf("owning index = %d; want 1 (parent)", idx)
	}
}

// TestResolveTemplate_ChildOnly covers the case where the parent
// doesn't ship the file at all and the child carries the whole
// template. Both common (child adds a new template parent never
// declared) and a regression check that a missing parent file
// doesn't blow up the walk.
func TestResolveTemplate_ChildOnly(t *testing.T) {
	t.Parallel()
	child := mapFiles{"taxonomy-genre.tsx": {}}
	parent := mapFiles{"index.tsx": {}}
	search := templates.SearchPath{child, parent}

	idx, ok := templates.ResolveTemplate("taxonomy-genre.tsx", search)
	if !ok {
		t.Fatalf("ResolveTemplate: expected hit on child, got miss")
	}
	if idx != 0 {
		t.Errorf("owning index = %d; want 0 (child)", idx)
	}
}

// TestResolveTemplate_ChildOverridesParent is the headline case: both
// theme roots ship the same filename, and the child must win because
// the walk order is child→parent.
func TestResolveTemplate_ChildOverridesParent(t *testing.T) {
	t.Parallel()
	child := mapFiles{"single.tsx": {}, "extra.tsx": {}}
	parent := mapFiles{"single.tsx": {}, "index.tsx": {}}
	search := templates.SearchPath{child, parent}

	idx, ok := templates.ResolveTemplate("single.tsx", search)
	if !ok {
		t.Fatalf("ResolveTemplate: expected hit, got miss")
	}
	if idx != 0 {
		t.Errorf("child must override parent; got owning index %d (want 0)", idx)
	}
}

// TestResolveTemplate_NilSearch documents the "pure function over an
// empty path" contract: no panic, no hit.
func TestResolveTemplate_NilSearch(t *testing.T) {
	t.Parallel()
	if _, ok := templates.ResolveTemplate("single.tsx", nil); ok {
		t.Errorf("ResolveTemplate(nil) reported a hit; want miss")
	}
}

// TestResolveTemplate_EmptyName guards against a caller accidentally
// passing the empty string (the result must be a miss, not a match
// on the empty filename).
func TestResolveTemplate_EmptyName(t *testing.T) {
	t.Parallel()
	files := mapFiles{"": {}}
	if _, ok := templates.ResolveTemplate("", templates.SearchPath{files}); ok {
		t.Errorf("ResolveTemplate(\"\") reported a hit; want miss")
	}
}

// TestResolveTemplate_SkipsNilEntry asserts the walker tolerates a
// nil ThemeFiles in the search path. Production callers may
// conditionally build the path (e.g. "child only if it exists on
// disk") and we don't want to force them to filter nils out.
func TestResolveTemplate_SkipsNilEntry(t *testing.T) {
	t.Parallel()
	parent := mapFiles{"single.tsx": {}}
	search := templates.SearchPath{nil, parent}

	idx, ok := templates.ResolveTemplate("single.tsx", search)
	if !ok {
		t.Fatalf("expected hit on parent past nil child")
	}
	if idx != 1 {
		t.Errorf("owning index = %d; want 1", idx)
	}
}

// TestResolveWithChild_PrecedenceWalk asserts the two-stage resolver
// walks the precedence list in order, dropping down to less-specific
// candidates only after every entry in the search path missed on
// the more-specific one. This is the WP child-theme contract: a
// parent's archive-book.tsx still loses to the child's single.tsx
// for a single-post request.
func TestResolveWithChild_PrecedenceWalk(t *testing.T) {
	t.Parallel()
	child := mapFiles{"single.tsx": {}}
	parent := mapFiles{"single-book.tsx": {}, "index.tsx": {}}
	search := templates.SearchPath{child, parent}
	req := templates.Request{
		Type:     templates.RequestTypeSingular,
		PostType: "book",
	}

	name, idx, err := templates.ResolveWithChild(req, search)
	if err != nil {
		t.Fatalf("ResolveWithChild: %v", err)
	}
	// The precedence list says single-book wins over plain single,
	// and the only single-book.* lives in the parent.
	if name != "single-book.tsx" {
		t.Errorf("name = %q; want %q", name, "single-book.tsx")
	}
	if idx != 1 {
		t.Errorf("owning index = %d; want 1 (parent)", idx)
	}
}

// TestResolveWithChild_ChildOverridesAtSameLevel asserts the child
// wins when both ship the same precedence-level template. This is
// the headline case for #46.
func TestResolveWithChild_ChildOverridesAtSameLevel(t *testing.T) {
	t.Parallel()
	child := mapFiles{"single.tsx": {}}
	parent := mapFiles{"single.tsx": {}, "index.tsx": {}}
	search := templates.SearchPath{child, parent}
	req := templates.Request{
		Type:     templates.RequestTypeSingular,
		PostType: "page",
	}

	name, idx, err := templates.ResolveWithChild(req, search)
	if err != nil {
		t.Fatalf("ResolveWithChild: %v", err)
	}
	if name != "single.tsx" {
		t.Errorf("name = %q; want %q", name, "single.tsx")
	}
	if idx != 0 {
		t.Errorf("child must override parent; owning index = %d (want 0)", idx)
	}
}

// TestResolveWithChild_FallsBackToIndex covers the ultimate-fallback
// branch — neither child nor parent ships any of the more-specific
// candidates, so the resolver lands on index.tsx in the parent.
func TestResolveWithChild_FallsBackToIndex(t *testing.T) {
	t.Parallel()
	child := mapFiles{}
	parent := mapFiles{"index.tsx": {}}
	search := templates.SearchPath{child, parent}
	req := templates.Request{Type: templates.RequestTypeSearch}

	name, idx, err := templates.ResolveWithChild(req, search)
	if err != nil {
		t.Fatalf("ResolveWithChild: %v", err)
	}
	if name != "index.tsx" {
		t.Errorf("name = %q; want index.tsx", name)
	}
	if idx != 1 {
		t.Errorf("owning index = %d; want 1 (parent)", idx)
	}
}

// TestResolveWithChild_ErrNoIndex asserts the contract that a search
// path missing index.* propagates the canonical sentinel error.
func TestResolveWithChild_ErrNoIndex(t *testing.T) {
	t.Parallel()
	search := templates.SearchPath{mapFiles{}, mapFiles{}}
	req := templates.Request{Type: templates.RequestTypeHome}
	_, _, err := templates.ResolveWithChild(req, search)
	if !errors.Is(err, templates.ErrNoIndex) {
		t.Errorf("err = %v; want ErrNoIndex", err)
	}
}

// TestResolveWithChild_UnknownRequestType asserts the unknown-type
// branch is reachable through the child-aware entry point too.
func TestResolveWithChild_UnknownRequestType(t *testing.T) {
	t.Parallel()
	search := templates.SearchPath{mapFiles{"index.tsx": {}}}
	_, _, err := templates.ResolveWithChild(templates.Request{}, search)
	if !errors.Is(err, templates.ErrUnknownRequestType) {
		t.Errorf("err = %v; want ErrUnknownRequestType", err)
	}
}
