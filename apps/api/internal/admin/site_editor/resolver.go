package site_editor

import (
	"context"
	"fmt"
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
)

// Resolver is the read-only public entry point the renderer plugs
// into. Given a (theme, name) pair it returns the resolved BlockTree
// — the override when one exists, the on-disk-parsed tree otherwise.
//
// Resolver caches parsed on-disk trees keyed by (theme, name) — the
// HTML→Block parse is pure CPU work and the part files don't change
// at runtime. Operator-saved overrides are NOT cached at this layer
// because the override store is itself cached (the settings store has
// an L1, and MemoryOverrideStore is in-memory anyway); double-caching
// would only widen the staleness window when an admin saves.
type Resolver struct {
	source   PartsSource
	override OverrideStore

	mu    sync.RWMutex
	cache map[string]BlockTree // key = theme + "/" + name
}

// NewResolver wires the resolver to its dependencies.
func NewResolver(source PartsSource, override OverrideStore) *Resolver {
	if source == nil {
		panic("site_editor: NewResolver: source is required")
	}
	if override == nil {
		panic("site_editor: NewResolver: override is required")
	}
	return &Resolver{
		source:   source,
		override: override,
		cache:    make(map[string]BlockTree),
	}
}

// Resolve returns the BlockTree for the named part. The boolean
// indicates whether the tree came from an override (true) or the
// on-disk file (false) — useful for the renderer's diagnostic
// headers, and for the admin UI's "Modified" badge when it calls
// Resolve directly.
//
// theme is the theme slug; pass the active theme. Passing the empty
// string is a programming error and returns an error rather than
// guessing — the renderer must thread the active slug explicitly so
// a future "preview another theme" flow has a single seam.
func (r *Resolver) Resolve(ctx context.Context, theme, name string) (BlockTree, bool, error) {
	if theme == "" {
		return nil, false, fmt.Errorf("site_editor: Resolve: theme is required")
	}
	if name == "" {
		return nil, false, fmt.Errorf("site_editor: Resolve: name is required")
	}

	if override, ok, err := r.override.Get(ctx, theme, name); err != nil {
		return nil, false, fmt.Errorf("site_editor: override lookup: %w", err)
	} else if ok {
		return override, true, nil
	}

	tree, err := r.diskTree(ctx, theme, name)
	if err != nil {
		return nil, false, err
	}
	return tree, false, nil
}

// InvalidateDisk clears the cached on-disk parse for (theme, name).
// Called by the theme-switch flow (when the active theme changes) and
// by the test suite. NOT called by the override Put/Delete path — the
// disk content didn't change, only the override did, and the resolver
// already short-circuits to the override on read.
func (r *Resolver) InvalidateDisk(theme, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, theme+"/"+name)
}

// InvalidateAll drops every cached on-disk parse. Used by the theme
// switcher when the active theme changes.
func (r *Resolver) InvalidateAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = make(map[string]BlockTree)
}

// diskTree returns the parsed on-disk BlockTree for (theme, name),
// populating the cache on first miss.
func (r *Resolver) diskTree(ctx context.Context, theme, name string) (BlockTree, error) {
	key := theme + "/" + name

	r.mu.RLock()
	if cached, ok := r.cache[key]; ok {
		out := make(BlockTree, len(cached))
		copy(out, cached)
		r.mu.RUnlock()
		return out, nil
	}
	r.mu.RUnlock()

	raw, err := r.source.Read(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("site_editor: read disk part %q: %w", name, err)
	}

	parsed, err := html2blocks.Convert(raw)
	if err != nil {
		return nil, fmt.Errorf("site_editor: parse disk part %q: %w", name, err)
	}
	tree := BlockTree(parsed)

	r.mu.Lock()
	r.cache[key] = tree
	r.mu.Unlock()

	out := make(BlockTree, len(tree))
	copy(out, tree)
	return out, nil
}
