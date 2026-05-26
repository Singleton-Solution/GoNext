package menus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the read/write surface shared by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TxBeginner is the optional transactional surface used by
// ReorderItems. A *pgxpool.Pool satisfies it.
type TxBeginner interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PgxStore is the production [Store] backed by Postgres.
type PgxStore struct {
	db TxBeginner
}

// NewPgxStore wraps a pool in the production Store. The pool is
// borrowed (not owned); the caller manages its lifecycle.
func NewPgxStore(db TxBeginner) *PgxStore {
	return &PgxStore{db: db}
}

const insertMenuSQL = `
INSERT INTO menus (slug, name, attrs)
VALUES ($1, $2, $3)
RETURNING id, created_at, updated_at
`

// CreateMenu implements [Store.CreateMenu].
func (s *PgxStore) CreateMenu(ctx context.Context, m Menu) (Menu, error) {
	if err := validateMenu(m); err != nil {
		return Menu{}, fmt.Errorf("%w: %s", ErrInvalidMenu, err.Error())
	}
	attrs := m.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	row := s.db.QueryRow(ctx, insertMenuSQL, m.Slug, m.Name, attrs)
	if err := row.Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return Menu{}, fmt.Errorf("menus: insert: %w", err)
	}
	m.Attrs = attrs
	return m, nil
}

const selectMenuByIDSQL = `
SELECT id, slug, name, attrs, created_at, updated_at FROM menus WHERE id = $1
`
const selectMenuBySlugSQL = `
SELECT id, slug, name, attrs, created_at, updated_at FROM menus WHERE slug = $1
`

func scanMenu(row scannable) (Menu, error) {
	var m Menu
	if err := row.Scan(&m.ID, &m.Slug, &m.Name, &m.Attrs, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return Menu{}, err
	}
	return m, nil
}

// GetMenu implements [Store.GetMenu].
func (s *PgxStore) GetMenu(ctx context.Context, id uuid.UUID) (Menu, error) {
	m, err := scanMenu(s.db.QueryRow(ctx, selectMenuByIDSQL, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Menu{}, fmt.Errorf("%w: id=%s", ErrNotFound, id)
		}
		return Menu{}, fmt.Errorf("menus: get: %w", err)
	}
	return m, nil
}

// GetMenuBySlug implements [Store.GetMenuBySlug].
func (s *PgxStore) GetMenuBySlug(ctx context.Context, slug string) (Menu, error) {
	m, err := scanMenu(s.db.QueryRow(ctx, selectMenuBySlugSQL, slug))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Menu{}, fmt.Errorf("%w: slug=%s", ErrNotFound, slug)
		}
		return Menu{}, fmt.Errorf("menus: get_by_slug: %w", err)
	}
	return m, nil
}

const updateMenuSQL = `
UPDATE menus SET name = $2, attrs = $3 WHERE id = $1
RETURNING id, slug, name, attrs, created_at, updated_at
`

// UpdateMenu implements [Store.UpdateMenu].
func (s *PgxStore) UpdateMenu(ctx context.Context, m Menu) (Menu, error) {
	if err := validateMenu(m); err != nil {
		return Menu{}, fmt.Errorf("%w: %s", ErrInvalidMenu, err.Error())
	}
	attrs := m.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	out, err := scanMenu(s.db.QueryRow(ctx, updateMenuSQL, m.ID, m.Name, attrs))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Menu{}, fmt.Errorf("%w: id=%s", ErrNotFound, m.ID)
		}
		return Menu{}, fmt.Errorf("menus: update: %w", err)
	}
	return out, nil
}

// DeleteMenu implements [Store.DeleteMenu]. ON DELETE CASCADE on
// menu_items.menu_id removes items automatically.
func (s *PgxStore) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM menus WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("menus: delete: %w", err)
	}
	return nil
}

const listMenusSQL = `
SELECT id, slug, name, attrs, created_at, updated_at
FROM menus ORDER BY name ASC
`

// ListMenus implements [Store.ListMenus].
func (s *PgxStore) ListMenus(ctx context.Context) ([]Menu, error) {
	rows, err := s.db.Query(ctx, listMenusSQL)
	if err != nil {
		return nil, fmt.Errorf("menus: list: %w", err)
	}
	defer rows.Close()
	var out []Menu
	for rows.Next() {
		m, err := scanMenu(rows)
		if err != nil {
			return nil, fmt.Errorf("menus: list_scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const insertItemSQL = `
INSERT INTO menu_items (menu_id, path, label, url, object_type, object_id, attrs)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at, updated_at
`

// CreateItem implements [Store.CreateItem].
func (s *PgxStore) CreateItem(ctx context.Context, mi MenuItem) (MenuItem, error) {
	if err := validateItem(mi); err != nil {
		return MenuItem{}, fmt.Errorf("%w: %s", ErrInvalidItem, err.Error())
	}
	attrs := mi.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	var objectType any
	if mi.ObjectType != "" {
		objectType = mi.ObjectType
	}
	var objectID any
	if mi.ObjectID != nil {
		objectID = *mi.ObjectID
	}
	row := s.db.QueryRow(ctx, insertItemSQL,
		mi.MenuID, mi.Path, mi.Label, mi.URL, objectType, objectID, attrs)
	if err := row.Scan(&mi.ID, &mi.CreatedAt, &mi.UpdatedAt); err != nil {
		return MenuItem{}, fmt.Errorf("menus: insert_item: %w", err)
	}
	mi.Attrs = attrs
	return mi, nil
}

const updateItemSQL = `
UPDATE menu_items
SET path = $2, label = $3, url = $4, object_type = $5, object_id = $6, attrs = $7
WHERE id = $1
RETURNING id, menu_id, path, label, url, object_type, object_id, attrs, created_at, updated_at
`

func scanItem(row scannable) (MenuItem, error) {
	var mi MenuItem
	var objectType *string
	var objectID *uuid.UUID
	if err := row.Scan(&mi.ID, &mi.MenuID, &mi.Path, &mi.Label, &mi.URL,
		&objectType, &objectID, &mi.Attrs, &mi.CreatedAt, &mi.UpdatedAt); err != nil {
		return MenuItem{}, err
	}
	if objectType != nil {
		mi.ObjectType = *objectType
	}
	mi.ObjectID = objectID
	return mi, nil
}

// UpdateItem implements [Store.UpdateItem].
func (s *PgxStore) UpdateItem(ctx context.Context, mi MenuItem) (MenuItem, error) {
	if err := validateItem(mi); err != nil {
		return MenuItem{}, fmt.Errorf("%w: %s", ErrInvalidItem, err.Error())
	}
	attrs := mi.Attrs
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	var objectType any
	if mi.ObjectType != "" {
		objectType = mi.ObjectType
	}
	var objectID any
	if mi.ObjectID != nil {
		objectID = *mi.ObjectID
	}
	out, err := scanItem(s.db.QueryRow(ctx, updateItemSQL,
		mi.ID, mi.Path, mi.Label, mi.URL, objectType, objectID, attrs))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MenuItem{}, fmt.Errorf("%w: id=%s", ErrNotFound, mi.ID)
		}
		return MenuItem{}, fmt.Errorf("menus: update_item: %w", err)
	}
	return out, nil
}

// DeleteItem implements [Store.DeleteItem].
func (s *PgxStore) DeleteItem(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM menu_items WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("menus: delete_item: %w", err)
	}
	return nil
}

// ReorderItems implements [Store.ReorderItems] as a single transaction.
//
// The unique (menu_id, path) constraint means we can't simply UPDATE
// each row in place if any two new paths conflict with old ones — the
// constraint check fires per-statement. We side-step by first prefixing
// every path with a sentinel character, then writing the real target
// paths. The sentinel form fails the regex CHECK, so we use the
// DEFERRABLE INITIALLY DEFERRED CHECK constraint approach via two-stage
// values: assign each item a temporary "001000.<i>"-style path inside
// the bounds of the regex, then move them to their final paths.
func (s *PgxStore) ReorderItems(ctx context.Context, menuID uuid.UUID, items []MenuItem) error {
	for _, mi := range items {
		if err := validateItem(mi); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidItem, err.Error())
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("menus: reorder begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Stage 1: park every item under a unique scratch path that won't
	// collide with anything in the user's payload. We use 999.NNN
	// suffixes — well above the realistic per-menu item count.
	for i, mi := range items {
		scratch := fmt.Sprintf("999.%03d", i+1)
		if _, err := tx.Exec(ctx,
			`UPDATE menu_items SET path = $2 WHERE id = $1 AND menu_id = $3`,
			mi.ID, scratch, menuID); err != nil {
			return fmt.Errorf("menus: reorder stage1: %w", err)
		}
	}
	// Stage 2: write the real target paths.
	for _, mi := range items {
		if _, err := tx.Exec(ctx,
			`UPDATE menu_items SET path = $2 WHERE id = $1 AND menu_id = $3`,
			mi.ID, mi.Path, menuID); err != nil {
			return fmt.Errorf("menus: reorder stage2: %w", err)
		}
	}
	return tx.Commit(ctx)
}

const listItemsSQL = `
SELECT id, menu_id, path, label, url, object_type, object_id, attrs, created_at, updated_at
FROM menu_items WHERE menu_id = $1 ORDER BY path ASC
`

// GetWithItems implements [Store.GetWithItems].
func (s *PgxStore) GetWithItems(ctx context.Context, id uuid.UUID) (MenuWithItems, error) {
	m, err := s.GetMenu(ctx, id)
	if err != nil {
		return MenuWithItems{}, err
	}
	rows, err := s.db.Query(ctx, listItemsSQL, id)
	if err != nil {
		return MenuWithItems{}, fmt.Errorf("menus: list_items: %w", err)
	}
	defer rows.Close()
	out := MenuWithItems{Menu: m}
	for rows.Next() {
		mi, err := scanItem(rows)
		if err != nil {
			return MenuWithItems{}, fmt.Errorf("menus: list_items_scan: %w", err)
		}
		out.Items = append(out.Items, mi)
	}
	return out, rows.Err()
}

// GetWithItemsBySlug implements [Store.GetWithItemsBySlug].
func (s *PgxStore) GetWithItemsBySlug(ctx context.Context, slug string) (MenuWithItems, error) {
	m, err := s.GetMenuBySlug(ctx, slug)
	if err != nil {
		return MenuWithItems{}, err
	}
	return s.GetWithItems(ctx, m.ID)
}

// scannable is the subset of pgx.Row / pgx.Rows that Scan needs.
type scannable interface {
	Scan(dest ...any) error
}
