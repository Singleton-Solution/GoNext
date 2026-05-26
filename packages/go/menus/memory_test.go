package menus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestMemoryStore_MenuCRUD(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	created, err := s.CreateMenu(ctx, Menu{Slug: "primary", Name: "Primary"})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatalf("CreateMenu: id not assigned")
	}

	got, err := s.GetMenuBySlug(ctx, "primary")
	if err != nil || got.ID != created.ID {
		t.Fatalf("GetMenuBySlug: got %+v err %v", got, err)
	}

	created.Name = "Updated"
	updated, err := s.UpdateMenu(ctx, created)
	if err != nil || updated.Name != "Updated" {
		t.Fatalf("UpdateMenu: %v", err)
	}

	all, err := s.ListMenus(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListMenus: got %d err %v", len(all), err)
	}

	if err := s.DeleteMenu(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMenu: %v", err)
	}
	if _, err := s.GetMenu(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStore_InvalidSlug(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.CreateMenu(context.Background(), Menu{Slug: "Bad Slug!", Name: "x"})
	if !errors.Is(err, ErrInvalidMenu) {
		t.Fatalf("expected ErrInvalidMenu, got %v", err)
	}
}

func TestMemoryStore_ItemsAndReorder(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	m, err := s.CreateMenu(ctx, Menu{Slug: "footer", Name: "Footer"})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	item1, err := s.CreateItem(ctx, MenuItem{MenuID: m.ID, Path: "001", Label: "Home", URL: "/"})
	if err != nil {
		t.Fatalf("CreateItem 1: %v", err)
	}
	item2, err := s.CreateItem(ctx, MenuItem{MenuID: m.ID, Path: "002", Label: "About", URL: "/about"})
	if err != nil {
		t.Fatalf("CreateItem 2: %v", err)
	}

	// Swap the two via reorder.
	item1.Path = "002"
	item2.Path = "001"
	if err := s.ReorderItems(ctx, m.ID, []MenuItem{item1, item2}); err != nil {
		t.Fatalf("ReorderItems: %v", err)
	}

	bundle, err := s.GetWithItems(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetWithItems: %v", err)
	}
	if len(bundle.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(bundle.Items))
	}
	if bundle.Items[0].Label != "About" || bundle.Items[1].Label != "Home" {
		t.Fatalf("reorder failed: %+v", bundle.Items)
	}
}

func TestMemoryStore_InvalidPath(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	m, _ := s.CreateMenu(ctx, Menu{Slug: "x", Name: "X"})
	_, err := s.CreateItem(ctx, MenuItem{MenuID: m.ID, Path: "bad", Label: "x"})
	if !errors.Is(err, ErrInvalidItem) {
		t.Fatalf("expected ErrInvalidItem for bad path, got %v", err)
	}
}

func TestMemoryStore_CascadeDelete(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	m, _ := s.CreateMenu(ctx, Menu{Slug: "x", Name: "X"})
	_, _ = s.CreateItem(ctx, MenuItem{MenuID: m.ID, Path: "001", Label: "x"})
	if err := s.DeleteMenu(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMenu: %v", err)
	}
	// After delete, the items map for this menu is gone — GetWithItems
	// returns ErrNotFound on the menu lookup.
	if _, err := s.GetWithItems(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_AttrsDefault(t *testing.T) {
	s := NewMemoryStore()
	m, _ := s.CreateMenu(context.Background(), Menu{Slug: "x", Name: "X"})
	if string(m.Attrs) != "{}" {
		t.Fatalf("expected default attrs '{}', got %q", string(m.Attrs))
	}
}

func TestMemoryStore_AttrsRoundTrip(t *testing.T) {
	s := NewMemoryStore()
	m, err := s.CreateMenu(context.Background(), Menu{
		Slug: "primary", Name: "P",
		Attrs: json.RawMessage(`{"location":"header"}`),
	})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if !json.Valid(m.Attrs) {
		t.Fatalf("attrs not valid JSON: %s", m.Attrs)
	}
}
