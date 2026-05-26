// Package menus is the read/write path for navigation menus and their
// items. Issue #54.
//
// Two top-level types: [Menu] (a named container) and [MenuItem] (a
// single link). Items carry a dot-separated ltree-style [MenuItem.Path]
// so a full menu can be loaded in sort order with a single ORDER BY,
// and a subtree can be loaded with a prefix-match.
//
// Two concrete stores ship:
//
//   - [MemoryStore]: backs tests and the no-DB development fallthrough.
//   - [PgxStore]: parameterised SQL against the menus + menu_items
//     tables (migration 000035).
//
// Renderer integration: the Navigation block resolves its `menu_id`
// attribute via [Store.GetWithItems] — one round trip, no N+1.
package menus

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Menu is a single named navigation container.
type Menu struct {
	ID        uuid.UUID       `json:"id"`
	Slug      string          `json:"slug"`
	Name      string          `json:"name"`
	Attrs     json.RawMessage `json:"attrs"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// MenuItem is a single link inside a [Menu]. [MenuItem.Path] is a
// dot-separated ltree-style ordering token; sort siblings and nest
// children by lexicographic compare.
type MenuItem struct {
	ID         uuid.UUID       `json:"id"`
	MenuID     uuid.UUID       `json:"menu_id"`
	Path       string          `json:"path"`
	Label      string          `json:"label"`
	URL        string          `json:"url"`
	ObjectType string          `json:"object_type,omitempty"`
	ObjectID   *uuid.UUID      `json:"object_id,omitempty"`
	Attrs      json.RawMessage `json:"attrs"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// MenuWithItems bundles a menu and its items for the single-trip
// renderer fetch.
type MenuWithItems struct {
	Menu  Menu       `json:"menu"`
	Items []MenuItem `json:"items"`
}

// Errors returned by Store implementations.
var (
	// ErrInvalidMenu is returned when [Menu] field validation fails
	// (empty slug, slug regex mismatch, name too long).
	ErrInvalidMenu = errors.New("menus: invalid menu")
	// ErrInvalidItem is returned when [MenuItem] field validation
	// fails (empty label, malformed path, oversize URL).
	ErrInvalidItem = errors.New("menus: invalid item")
	// ErrNotFound is returned by Get/Update/Delete when no row
	// matches the requested ID or slug.
	ErrNotFound = errors.New("menus: not found")
)

// Store is the persistence contract. Implementations MUST be safe
// for concurrent use.
type Store interface {
	// CreateMenu inserts a new menu. ID is assigned by the store.
	CreateMenu(ctx context.Context, m Menu) (Menu, error)
	// GetMenu fetches a menu by ID.
	GetMenu(ctx context.Context, id uuid.UUID) (Menu, error)
	// GetMenuBySlug resolves a menu by its stable slug. The
	// Navigation block uses this when the author has pinned a slug
	// rather than a UUID in the block attributes.
	GetMenuBySlug(ctx context.Context, slug string) (Menu, error)
	// UpdateMenu mutates name/attrs on an existing menu. Slug is
	// immutable post-create — callers wanting to rename make a new
	// menu and migrate items.
	UpdateMenu(ctx context.Context, m Menu) (Menu, error)
	// DeleteMenu removes a menu and (via ON DELETE CASCADE) every
	// item belonging to it.
	DeleteMenu(ctx context.Context, id uuid.UUID) error
	// ListMenus returns every menu sorted by name.
	ListMenus(ctx context.Context) ([]Menu, error)

	// CreateItem inserts a new menu item. Path must be supplied by
	// the caller (typically derived from the drag-drop position).
	CreateItem(ctx context.Context, mi MenuItem) (MenuItem, error)
	// UpdateItem mutates label/url/object_type/object_id/attrs/path.
	UpdateItem(ctx context.Context, mi MenuItem) (MenuItem, error)
	// DeleteItem removes a single item by ID. Subtrees must be
	// re-pathed by the caller before deletion.
	DeleteItem(ctx context.Context, id uuid.UUID) error
	// ReorderItems atomically rewrites the path of multiple items
	// (the drag-drop "move + reorder" operation). All items must
	// belong to the same menu_id.
	ReorderItems(ctx context.Context, menuID uuid.UUID, items []MenuItem) error

	// GetWithItems is the single-trip renderer fetch.
	GetWithItems(ctx context.Context, id uuid.UUID) (MenuWithItems, error)
	// GetWithItemsBySlug is the slug-keyed variant.
	GetWithItemsBySlug(ctx context.Context, slug string) (MenuWithItems, error)
}

// validateMenu enforces the column CHECK rules in code so the memory
// store and the Postgres store fail the same way for the same input.
func validateMenu(m Menu) error {
	if len(m.Slug) == 0 || len(m.Slug) > 64 {
		return errors.New("menus: invalid menu: slug length out of range")
	}
	if !slugRe.MatchString(m.Slug) {
		return errors.New("menus: invalid menu: slug must match ^[a-z0-9][a-z0-9_-]*$")
	}
	if len(m.Name) == 0 || len(m.Name) > 128 {
		return errors.New("menus: invalid menu: name length out of range")
	}
	if len(m.Attrs) > 0 && !json.Valid(m.Attrs) {
		return errors.New("menus: invalid menu: attrs not valid JSON")
	}
	return nil
}

// validateItem enforces the menu_items CHECK rules in code.
func validateItem(mi MenuItem) error {
	if len(mi.Label) == 0 || len(mi.Label) > 256 {
		return errors.New("menus: invalid item: label length out of range")
	}
	if len(mi.Path) == 0 || len(mi.Path) > 256 {
		return errors.New("menus: invalid item: path length out of range")
	}
	if !pathRe.MatchString(mi.Path) {
		return errors.New("menus: invalid item: path must match ^[0-9]{3}(\\.[0-9]{3})*$")
	}
	if len(mi.URL) > 2048 {
		return errors.New("menus: invalid item: url too long")
	}
	if mi.ObjectType != "" {
		switch mi.ObjectType {
		case "post", "page", "term", "custom":
		default:
			return errors.New("menus: invalid item: object_type must be one of post|page|term|custom")
		}
	}
	if len(mi.Attrs) > 0 && !json.Valid(mi.Attrs) {
		return errors.New("menus: invalid item: attrs not valid JSON")
	}
	return nil
}
