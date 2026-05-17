package posts

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryStore_CreateAndGet(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()

	title := "Hello"
	slug := "hello"
	in := CreateInput{Title: &title, Slug: &slug}
	p, err := s.Create(ctx, PostTypePost, "user-1", in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" {
		t.Errorf("Create: empty id")
	}
	if p.Title != "Hello" {
		t.Errorf("Create: title = %q", p.Title)
	}
	if p.AuthorID != "user-1" {
		t.Errorf("Create: author = %q", p.AuthorID)
	}
	if p.Version != 1 {
		t.Errorf("Create: version = %d, want 1", p.Version)
	}
	if len(p.Hash()) == 0 {
		t.Errorf("Create: content_blocks_hash empty")
	}

	got, err := s.Get(ctx, PostTypePost, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Hello" {
		t.Errorf("Get: title = %q", got.Title)
	}
}

func TestMemoryStore_Get_NotFoundForWrongType(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()
	p, err := s.Create(ctx, PostTypePost, "user-1", CreateInput{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Get(ctx, PostTypePage, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get cross-type = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_Update_BumpsVersion(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()
	p, err := s.Create(ctx, PostTypePost, "user-1", CreateInput{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTitle := "Updated"
	updated, err := s.Update(ctx, PostTypePost, p.ID, p.Version, UpdateInput{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != p.Version+1 {
		t.Errorf("version after update = %d, want %d", updated.Version, p.Version+1)
	}
	if updated.Title != "Updated" {
		t.Errorf("title = %q, want Updated", updated.Title)
	}
}

func TestMemoryStore_Update_VersionConflict(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()
	p, err := s.Create(ctx, PostTypePost, "user-1", CreateInput{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = s.Update(ctx, PostTypePost, p.ID, 99, UpdateInput{})
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("Update wrong version = %v, want ErrVersionConflict", err)
	}
}

func TestMemoryStore_Trash(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()
	p, err := s.Create(ctx, PostTypePost, "user-1", CreateInput{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trashed, err := s.Trash(ctx, PostTypePost, p.ID, p.Version)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if trashed.Status != "trash" {
		t.Errorf("status = %q, want trash", trashed.Status)
	}
}

func TestMemoryStore_DuplicateSlug(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()
	slug := "my-post"
	if _, err := s.Create(ctx, PostTypePost, "u1", CreateInput{Slug: &slug}); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if _, err := s.Create(ctx, PostTypePost, "u2", CreateInput{Slug: &slug}); !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("Create 2 = %v, want ErrDuplicateSlug", err)
	}
	// Same slug under a different post_type is fine.
	if _, err := s.Create(ctx, PostTypePage, "u1", CreateInput{Slug: &slug}); err != nil {
		t.Errorf("Create page with same slug as post: %v", err)
	}
}

func TestMemoryStore_List_FilterByStatus(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	ctx := context.Background()
	draft := "draft"
	pub := "published"
	if _, err := s.Create(ctx, PostTypePost, "u1", CreateInput{Status: &draft}); err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	if _, err := s.Create(ctx, PostTypePost, "u1", CreateInput{Status: &pub}); err != nil {
		t.Fatalf("Create pub: %v", err)
	}
	rows, err := s.List(ctx, PostTypePost, ListFilter{Status: "published", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != "published" {
		t.Errorf("rows = %v", rows)
	}
}

func TestMemoryStore_List_Cursor(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	// Deterministic ids so the cursor order is predictable.
	var n int
	s.SetIDFunc(func() string {
		n++
		// Fixed-width so lexicographic sort matches numeric.
		return formatTestID(n)
	})

	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if _, err := s.Create(ctx, PostTypePost, "u1", CreateInput{}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	// First page.
	rows, err := s.List(ctx, PostTypePost, ListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	// limit+1 so the handler can detect more.
	if len(rows) != 21 {
		t.Errorf("page 1 len = %d, want 21 (limit+1 for paging)", len(rows))
	}

	// Second page using last id as cursor.
	rows2, err := s.List(ctx, PostTypePost, ListFilter{Limit: 20, After: rows[19].ID})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(rows2) != 21 {
		t.Errorf("page 2 len = %d, want 21", len(rows2))
	}
	// No overlap between pages.
	if rows[19].ID >= rows2[0].ID {
		t.Errorf("cursor did not advance: %s vs %s", rows[19].ID, rows2[0].ID)
	}
}

func formatTestID(n int) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = hex[n%16]
		n /= 16
	}
	return string(out)
}
