package site_editor

import (
	"context"
	"errors"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
)

// Block is the on-wire block shape. Aliased to html2blocks.Block so the
// converter's output round-trips through the API without an extra copy
// — the JSON tags already match the editor's persisted form.
//
// We deliberately do NOT re-declare the type here even though it lives
// in a migration package: when packages/go/blocks lands (#352) the
// alias swaps in one place rather than spread across every site_editor
// file.
type Block = html2blocks.Block

// BlockTree is the slice form the editor passes back and forth. Stored
// verbatim in the override store; the renderer walks it the same way
// it walks the on-disk parts after parse.
type BlockTree []Block

// Part is one template part as surfaced to the admin UI. The shape is
// intentionally flat: name + title + area are the metadata the
// theme.json declares, blocks is the resolved BlockTree, and
// overridden is the badge the UI renders ("Reset to default" only
// makes sense when there's something to reset to).
type Part struct {
	// Name is the slug under themes/{theme}/parts/{name}.html — e.g.
	// "header", "footer", "sidebar". The renderer keys off this name
	// when looking up the override.
	Name string `json:"name"`

	// Title is the human-readable label the admin UI renders in the
	// sidebar. Pulled from theme.json#templateParts; falls back to
	// Name when unset.
	Title string `json:"title"`

	// Area is the canonical part area declared by theme.json
	// ("header", "footer", "sidebar", "uncategorized"). The admin UI
	// groups the sidebar by area when there are more than a handful
	// of parts.
	Area string `json:"area"`

	// Blocks is the resolved BlockTree — the override if present, the
	// disk-parsed tree otherwise.
	Blocks BlockTree `json:"blocks"`

	// Overridden is true when Blocks came from the override store
	// rather than the on-disk file. The UI uses this to render the
	// "Modified" badge and enable the "Reset to default" button.
	Overridden bool `json:"overridden"`
}

// PartsSource is the read-only abstraction over the active theme's
// on-disk parts. Production wraps an os.DirFS rooted at
// themes/{active}/parts; tests use an in-memory implementation. The
// interface is intentionally tiny — list + read — so the handler
// stays decoupled from the eventual theme-discovery surface (issue
// #319 ships that as a separate Registry concept; we don't want
// site_editor to grow a hard dependency on it).
type PartsSource interface {
	// ActiveTheme returns the slug of the active theme. Used as the
	// namespace key into the override store.
	ActiveTheme(ctx context.Context) (string, error)

	// List returns the declared parts for the active theme — the
	// theme.json#templateParts surface. Each entry carries the
	// metadata (name + title + area) but NO content; the handler
	// loads content via Read.
	List(ctx context.Context) ([]PartMeta, error)

	// Read returns the raw on-disk HTML bytes for the named part. The
	// returned slice is owned by the caller; sources MUST return a
	// fresh copy on each call (no shared mutable state).
	//
	// Returns ErrPartNotFound when the named part is not in the
	// theme's declared inventory.
	Read(ctx context.Context, name string) ([]byte, error)
}

// PartMeta is the metadata side of a Part — the theme.json declaration
// without the resolved BlockTree. The list-parts handler joins this
// with the override store + on-disk bytes to produce a full Part.
type PartMeta struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Area  string `json:"area"`
}

// OverrideStore is the persistence surface for operator-saved
// overrides. Production wraps the options table at the key
//
//	theme_mods.{theme}.parts.{name}
//
// — one row per (theme, part) pair. The wrapping is intentional: the
// settings store doesn't expose a "list keys under a prefix" verb, so
// the override store keeps its own enumeration in-memory for the
// active theme, scanning the options table once at boot.
//
// All methods take the theme slug as their first argument so a future
// "preview another theme" flow has a place to plug in without
// breaking the interface.
type OverrideStore interface {
	// Get returns the override BlockTree for (theme, name). The
	// boolean is true iff an override exists; a missing override is
	// NOT an error.
	Get(ctx context.Context, theme, name string) (BlockTree, bool, error)

	// Put persists the override BlockTree for (theme, name),
	// upserting any prior override. Validation (block names exist in
	// the registry) is the caller's responsibility — the store
	// trusts the bytes it's handed.
	Put(ctx context.Context, theme, name string, tree BlockTree) error

	// Delete removes the override for (theme, name). Idempotent: a
	// missing override is not an error.
	Delete(ctx context.Context, theme, name string) error
}

// BlockValidator validates that every block in a tree resolves to a
// registered block name. Wraps the editor's registry without forcing
// site_editor to take a transitive dependency on the full registry
// machinery — a tiny function pointer is enough for the validation
// site_editor needs.
//
// The default validator (NewDefaultBlockValidator) accepts the
// canonical core block names from html2blocks.Block* and an explicit
// allowlist of theme-shipped names (core/group, core/site-title,
// core/site-tagline, core/navigation, core/site-logo, core/search,
// core/site-tagline). Plugins extend the allowlist via Register.
type BlockValidator interface {
	// Validate walks the tree and returns nil iff every block name
	// (including nested innerBlocks) is registered. The error
	// surfaces the offending name so the operator sees which block
	// the editor saved that the renderer can't render.
	Validate(tree BlockTree) error
}

// ErrPartNotFound is returned by PartsSource.Read when the requested
// part is not in the active theme's declared inventory.
var ErrPartNotFound = errors.New("site_editor: part not found")

// ErrUnknownBlock is the sentinel returned by BlockValidator.Validate
// when a tree contains a block name the validator doesn't recognise.
// Wrapped errors include the offending name in their Error() text.
var ErrUnknownBlock = errors.New("site_editor: unknown block name")
