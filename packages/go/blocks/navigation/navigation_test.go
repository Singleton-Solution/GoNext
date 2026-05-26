package navigation

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/packages/go/blocks/render"
	"github.com/Singleton-Solution/GoNext/packages/go/menus"
)

func TestRenderNavigation_WithResolver(t *testing.T) {
	store := menus.NewMemoryStore()
	m, err := store.CreateMenu(context.Background(), menus.Menu{Slug: "primary", Name: "Primary"})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if _, err := store.CreateItem(context.Background(), menus.MenuItem{
		MenuID: m.ID, Path: "001", Label: "Home", URL: "/",
	}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if _, err := store.CreateItem(context.Background(), menus.MenuItem{
		MenuID: m.ID, Path: "002", Label: "About", URL: "/about",
	}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	resolver := NewStoreResolver(store)
	ctx := render.Context{ContextKeyMenuResolver: resolver}
	block := render.Block{
		Attributes: map[string]any{"menu_id": m.ID.String()},
	}
	out, err := renderNav(block, "", ctx)
	if err != nil {
		t.Fatalf("renderNav: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<a href="/">Home</a>`) {
		t.Fatalf("expected Home anchor: %s", got)
	}
	if !strings.Contains(got, `<a href="/about">About</a>`) {
		t.Fatalf("expected About anchor: %s", got)
	}
}

func TestRenderNavigation_BySlug(t *testing.T) {
	store := menus.NewMemoryStore()
	m, _ := store.CreateMenu(context.Background(), menus.Menu{Slug: "footer", Name: "F"})
	_, _ = store.CreateItem(context.Background(), menus.MenuItem{
		MenuID: m.ID, Path: "001", Label: "Privacy", URL: "/privacy",
	})

	resolver := NewStoreResolver(store)
	ctx := render.Context{ContextKeyMenuResolver: resolver}
	block := render.Block{Attributes: map[string]any{"menu_slug": "footer"}}
	out, _ := renderNav(block, "", ctx)
	if !strings.Contains(string(out), "Privacy") {
		t.Fatalf("expected Privacy in slug-resolved menu: %s", out)
	}
}

func TestRenderNavigation_NoResolver(t *testing.T) {
	out, _ := renderNav(render.Block{Attributes: map[string]any{"menu_id": uuid.New().String()}}, "", render.Context{})
	if !strings.Contains(string(out), "no menuResolver") {
		t.Fatalf("expected comment about missing resolver: %s", out)
	}
}

func TestRenderNavigation_MissingMenu(t *testing.T) {
	store := menus.NewMemoryStore()
	resolver := NewStoreResolver(store)
	ctx := render.Context{ContextKeyMenuResolver: resolver}
	block := render.Block{Attributes: map[string]any{"menu_id": uuid.New().String()}}
	out, _ := renderNav(block, "", ctx)
	if !strings.Contains(string(out), "resolve failed") {
		t.Fatalf("expected resolve failed comment: %s", out)
	}
}
