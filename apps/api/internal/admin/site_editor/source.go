package site_editor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"sync"
)

// FSPartsSource is the production PartsSource. It enumerates the
// active theme's parts by scanning a filesystem (typically an
// os.DirFS rooted at themes/{active}/parts/), and resolves part
// metadata from a theme.json#templateParts declaration handed in at
// construction.
//
// The active theme slug is supplied as a function so the source
// transparently picks up theme switches without the operator having
// to reboot the API — the next List/Read call sees the new theme.
type FSPartsSource struct {
	// fsys is the filesystem rooted at the theme's parts/ directory.
	// Production passes os.DirFS("themes/" + slug + "/parts"); tests
	// pass a fstest.MapFS.
	fsys fs.FS

	// activeFn returns the active theme's slug. Called once per
	// PartsSource method invocation so a mid-request theme switch
	// surfaces immediately. nil means "no theme is active" — every
	// method returns ErrNoActiveTheme in that case.
	activeFn func(context.Context) (string, error)

	// meta is the theme.json#templateParts surface for the active
	// theme. The renderer doesn't strictly need it (it walks the
	// filesystem), but the editor's sidebar uses Title + Area to
	// label parts, so we cache the declaration here. Refreshed
	// whenever the active theme changes (see refreshMeta).
	mu       sync.RWMutex
	metaSlug string
	meta     []PartMeta
	metaFn   func(ctx context.Context, theme string) ([]PartMeta, error)
}

// NewFSPartsSource constructs an FSPartsSource. fsys is the
// filesystem rooted at the active theme's parts/ directory; activeFn
// returns the slug of the active theme; metaFn returns the
// theme.json#templateParts declaration for the named theme.
//
// All three must be non-nil. Passing a nil callback is a programming
// error and panics at construction (rather than at the first request),
// because the wiring happens at boot and that's the right time to
// catch a missing dependency.
func NewFSPartsSource(
	fsys fs.FS,
	activeFn func(context.Context) (string, error),
	metaFn func(ctx context.Context, theme string) ([]PartMeta, error),
) *FSPartsSource {
	if fsys == nil {
		panic("site_editor: NewFSPartsSource: fsys is required")
	}
	if activeFn == nil {
		panic("site_editor: NewFSPartsSource: activeFn is required")
	}
	if metaFn == nil {
		panic("site_editor: NewFSPartsSource: metaFn is required")
	}
	return &FSPartsSource{
		fsys:     fsys,
		activeFn: activeFn,
		metaFn:   metaFn,
	}
}

// ActiveTheme returns the active theme slug.
func (s *FSPartsSource) ActiveTheme(ctx context.Context) (string, error) {
	slug, err := s.activeFn(ctx)
	if err != nil {
		return "", fmt.Errorf("site_editor: active theme: %w", err)
	}
	if slug == "" {
		return "", ErrNoActiveTheme
	}
	return slug, nil
}

// List returns the active theme's declared parts. The result is the
// theme.json#templateParts declaration cached by slug — a theme switch
// invalidates the cache. The slice is sorted by Name so the admin UI
// renders a stable ordering.
func (s *FSPartsSource) List(ctx context.Context) ([]PartMeta, error) {
	slug, err := s.ActiveTheme(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := s.metaForSlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	out := make([]PartMeta, len(meta))
	copy(out, meta)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read returns the raw HTML bytes for the named part. Returns
// ErrPartNotFound when the part isn't in the theme's declared
// inventory — even if a file with that name exists on disk, we
// require the theme to have declared it so we never surface an
// unmanaged blob.
func (s *FSPartsSource) Read(ctx context.Context, name string) ([]byte, error) {
	slug, err := s.ActiveTheme(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := s.metaForSlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !hasMeta(meta, name) {
		return nil, ErrPartNotFound
	}
	b, err := fs.ReadFile(s.fsys, name+".html")
	if errors.Is(err, fs.ErrNotExist) {
		// Declared in theme.json but the file isn't there. The editor
		// should still be able to open a blank canvas for it, so we
		// return an empty body rather than ErrPartNotFound.
		return []byte{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("site_editor: read part %q: %w", name, err)
	}
	// Defensive copy so callers can't mutate the underlying slice.
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// metaForSlug returns the cached metadata for the slug, populating
// the cache on first hit or on slug change.
func (s *FSPartsSource) metaForSlug(ctx context.Context, slug string) ([]PartMeta, error) {
	s.mu.RLock()
	cached, ok := s.metaSlug == slug, s.meta != nil
	if ok && cached {
		out := s.meta
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	meta, err := s.metaFn(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("site_editor: load templateParts for %q: %w", slug, err)
	}

	s.mu.Lock()
	s.metaSlug = slug
	s.meta = meta
	s.mu.Unlock()
	return meta, nil
}

func hasMeta(meta []PartMeta, name string) bool {
	for _, m := range meta {
		if m.Name == name {
			return true
		}
	}
	return false
}

// ErrNoActiveTheme is returned when no theme is active. Almost always
// indicates a misconfigured install — the seeder should have run at
// boot and set core.active_theme to gn-hello.
var ErrNoActiveTheme = errors.New("site_editor: no active theme")
