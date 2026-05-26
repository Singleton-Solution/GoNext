package collections

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

// newStore returns a MemoryStore with a deterministic clock and id
// generator so test assertions on path / id can be exact.
func newStore() *MemoryStore {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ck int
	clock := func() time.Time {
		ck++
		return base.Add(time.Duration(ck) * time.Second)
	}
	var seq int
	idGen := func() string {
		seq++
		return "col-" + strconv.Itoa(seq)
	}
	return NewMemoryStore(clock, idGen)
}

func TestCreateRoot(t *testing.T) {
	s := newStore()
	c, err := s.Create(context.Background(), CreateInput{Slug: "marketing", Name: "Marketing"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Path != "marketing" {
		t.Errorf("Path = %q, want %q", c.Path, "marketing")
	}
	if c.ParentID != nil {
		t.Errorf("ParentID = %v, want nil", *c.ParentID)
	}
}

func TestCreateChildAppendsPath(t *testing.T) {
	s := newStore()
	parent, _ := s.Create(context.Background(), CreateInput{Slug: "marketing", Name: "Marketing"})
	child, err := s.Create(context.Background(), CreateInput{Slug: "q1", Name: "Q1", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if child.Path != "marketing.q1" {
		t.Errorf("Path = %q, want %q", child.Path, "marketing.q1")
	}
	if child.Depth() != 1 {
		t.Errorf("Depth = %d, want 1", child.Depth())
	}
}

func TestCreateRejectsInvalidSlug(t *testing.T) {
	s := newStore()
	for _, slug := range []string{"", "Has-Capital", "_starts-with-underscore", "-leading-hyphen", "x!", "with space"} {
		_, err := s.Create(context.Background(), CreateInput{Slug: slug, Name: "X"})
		if !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("slug %q: expected ErrInvalidSlug, got %v", slug, err)
		}
	}
}

func TestCreateRejectsSiblingSlugCollision(t *testing.T) {
	s := newStore()
	_, _ = s.Create(context.Background(), CreateInput{Slug: "marketing", Name: "Marketing"})
	_, err := s.Create(context.Background(), CreateInput{Slug: "marketing", Name: "Other"})
	if !errors.Is(err, ErrSlugConflict) {
		t.Fatalf("expected ErrSlugConflict, got %v", err)
	}
}

func TestCreateAllowsSameSlugUnderDifferentParents(t *testing.T) {
	s := newStore()
	a, _ := s.Create(context.Background(), CreateInput{Slug: "a", Name: "A"})
	b, _ := s.Create(context.Background(), CreateInput{Slug: "b", Name: "B"})
	_, err := s.Create(context.Background(), CreateInput{Slug: "kids", Name: "Kids", ParentID: &a.ID})
	if err != nil {
		t.Fatalf("first kids: %v", err)
	}
	_, err = s.Create(context.Background(), CreateInput{Slug: "kids", Name: "Kids", ParentID: &b.ID})
	if err != nil {
		t.Fatalf("second kids: %v", err)
	}
}

func TestMoveRewritesDescendantPaths(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, CreateInput{Slug: "a", Name: "A"})
	aChild, _ := s.Create(ctx, CreateInput{Slug: "child", Name: "Child", ParentID: &a.ID})
	aGrand, _ := s.Create(ctx, CreateInput{Slug: "grand", Name: "Grand", ParentID: &aChild.ID})
	b, _ := s.Create(ctx, CreateInput{Slug: "b", Name: "B"})

	if _, err := s.Move(ctx, aChild.ID, MoveInput{NewParentID: &b.ID}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	moved, _ := s.GetByID(ctx, aChild.ID)
	if moved.Path != "b.child" {
		t.Errorf("moved.Path = %q, want %q", moved.Path, "b.child")
	}
	grand, _ := s.GetByID(ctx, aGrand.ID)
	if grand.Path != "b.child.grand" {
		t.Errorf("grand.Path = %q, want %q", grand.Path, "b.child.grand")
	}
}

func TestMoveRejectsCycle(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, CreateInput{Slug: "a", Name: "A"})
	aChild, _ := s.Create(ctx, CreateInput{Slug: "child", Name: "Child", ParentID: &a.ID})
	// Move a under its own child -> cycle.
	_, err := s.Move(ctx, a.ID, MoveInput{NewParentID: &aChild.ID})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
	// Move a under itself -> cycle.
	_, err = s.Move(ctx, a.ID, MoveInput{NewParentID: &a.ID})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("expected ErrCycle (self), got %v", err)
	}
}

func TestRenameSlugRewritesPath(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, CreateInput{Slug: "old", Name: "Old"})
	child, _ := s.Create(ctx, CreateInput{Slug: "child", Name: "Child", ParentID: &a.ID})

	newSlug := "new"
	if _, err := s.Rename(ctx, a.ID, UpdateInput{Slug: &newSlug}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	parent, _ := s.GetByID(ctx, a.ID)
	if parent.Path != "new" {
		t.Errorf("parent.Path = %q, want %q", parent.Path, "new")
	}
	got, _ := s.GetByID(ctx, child.ID)
	if got.Path != "new.child" {
		t.Errorf("child.Path = %q, want %q", got.Path, "new.child")
	}
}

func TestDeleteCascades(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, CreateInput{Slug: "a", Name: "A"})
	child, _ := s.Create(ctx, CreateInput{Slug: "child", Name: "Child", ParentID: &a.ID})
	grand, _ := s.Create(ctx, CreateInput{Slug: "grand", Name: "Grand", ParentID: &child.ID})

	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, id := range []string{a.ID, child.ID, grand.ID} {
		if _, err := s.GetByID(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("id %s: expected ErrNotFound, got %v", id, err)
		}
	}
}

func TestDescendantsIncludesSelf(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, CreateInput{Slug: "a", Name: "A"})
	child, _ := s.Create(ctx, CreateInput{Slug: "child", Name: "Child", ParentID: &a.ID})
	out, err := s.Descendants(ctx, a.Path)
	if err != nil {
		t.Fatalf("Descendants: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].ID != a.ID || out[1].ID != child.ID {
		t.Errorf("unexpected order: %#v", out)
	}
}

func TestChildren(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	a, _ := s.Create(ctx, CreateInput{Slug: "a", Name: "A"})
	_, _ = s.Create(ctx, CreateInput{Slug: "b", Name: "B"})
	_, _ = s.Create(ctx, CreateInput{Slug: "x", Name: "X", ParentID: &a.ID})
	_, _ = s.Create(ctx, CreateInput{Slug: "y", Name: "Y", ParentID: &a.ID})

	roots, _ := s.Children(ctx, nil)
	if len(roots) != 2 {
		t.Errorf("roots = %d, want 2", len(roots))
	}
	kids, _ := s.Children(ctx, &a.ID)
	if len(kids) != 2 {
		t.Errorf("kids = %d, want 2", len(kids))
	}
}

func TestMaxDepth(t *testing.T) {
	ctx := context.Background()
	s := newStore()
	current, _ := s.Create(ctx, CreateInput{Slug: "d0", Name: "D0"})
	for i := 1; i < MaxDepth; i++ {
		next, err := s.Create(ctx, CreateInput{Slug: "d" + strconv.Itoa(i), Name: "D" + strconv.Itoa(i), ParentID: &current.ID})
		if err != nil {
			t.Fatalf("level %d: %v", i, err)
		}
		current = next
	}
	// One more should fail.
	_, err := s.Create(ctx, CreateInput{Slug: "toodeep", Name: "Too", ParentID: &current.ID})
	if !errors.Is(err, ErrTooDeep) {
		t.Fatalf("expected ErrTooDeep, got %v", err)
	}
}
