package wprest

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Deps is the dependency bundle passed to [Mount].
//
// The shim is built as a translation layer on top of small read-only
// "source" interfaces — one per resource type — rather than pulling in
// the native rest/posts package directly. The seams keep the shim
// testable with simple fakes, and they let production wire production
// stores while tests substitute deterministic in-memory ones.
type Deps struct {
	// Posts sources `post` rows for /wp-json/wp/v2/posts.
	Posts PostSource

	// Pages sources `page` rows for /wp-json/wp/v2/pages. Pages and
	// posts have identical shape; the discriminator is the source.
	Pages PostSource

	// Users sources user rows for /wp-json/wp/v2/users. May be nil; if
	// nil, the users routes return an empty collection (live WP behavior
	// when the public-users surface is disabled).
	Users UserSource

	// Categories sources category terms for /wp-json/wp/v2/categories.
	// May be nil → empty collection.
	Categories TermSource

	// Tags sources tag terms for /wp-json/wp/v2/tags. May be nil →
	// empty collection.
	Tags TermSource

	// SiteURL is the absolute site base, e.g. "https://example.com".
	// Used to construct `link` URLs and `_links` entries. Must not have
	// a trailing slash. Required.
	SiteURL string

	// Logger receives structured log lines. nil falls back to slog.Default
	// at Mount time.
	Logger *slog.Logger
}

// validate returns an error when Deps is misconfigured. Called by
// [Mount] to fail fast on boot rather than crash at request time.
func (d Deps) validate() error {
	if d.Posts == nil {
		return errors.New("wprest.Mount: Deps.Posts is required")
	}
	if d.Pages == nil {
		return errors.New("wprest.Mount: Deps.Pages is required")
	}
	if d.SiteURL == "" {
		return errors.New("wprest.Mount: Deps.SiteURL is required")
	}
	return nil
}

// PostSource is the abstraction the shim uses to read post/page rows.
// Implementations adapt the underlying GoNext store (or a test fake)
// into the small set of operations the shim actually needs.
//
// The shim never writes; this interface intentionally exposes only read
// methods. A future "write shim" issue (out of scope for #89) will add
// a parallel PostSink interface.
type PostSource interface {
	// List returns post rows matching filter. Filter semantics are the
	// WP-translated ones: integer category/tag ids (legacy_int_id),
	// rendered-content search, etc. Implementations apply the filter +
	// ordering; the shim then paginates the returned slice in-memory.
	List(ctx context.Context, filter PostFilter) ([]PostRow, error)

	// GetByLegacyID returns a single row by its legacy_int_id, the
	// integer used in WP URLs. Returns ErrNotFound for unknown ids.
	GetByLegacyID(ctx context.Context, id int) (PostRow, error)
}

// PostRow is the WP-shim-shaped projection of a post row. Field names
// are kept close to the WP envelope so the translator (posts.go) is a
// straightforward field mapping.
type PostRow struct {
	LegacyID      int
	Slug          string
	Status        string
	Type          string // "post" or "page"
	Title         string
	ContentHTML   string
	ExcerptHTML   string
	AuthorID      int
	FeaturedMedia int
	CommentStatus string
	PingStatus    string
	Sticky        bool
	Template      string
	Format        string
	Categories    []int
	Tags          []int
	Date          time.Time
	DateGMT       time.Time
	Modified      time.Time
	ModifiedGMT   time.Time
	Protected     bool
}

// PostFilter is the input shape to PostSource.List. Empty slices mean
// "no filter for this field"; a non-nil but zero-length slice (which
// Go's zero-value semantics make hard to construct accidentally) is
// treated the same as nil.
type PostFilter struct {
	Search     string
	Slug       string
	Statuses   []string // when empty, implementations default to ["publish"]
	Categories []int
	Tags       []int
	OrderBy    string // date|title|id|slug|modified
	Order      string // asc|desc
}

// UserSource exposes the user-listing surface used by the WP users
// shim. The shim emits the *public* user fields only (id, name, slug,
// link, description, avatar_urls) — sensitive fields (email, roles,
// capabilities) are gated behind authentication, which is out of scope
// for this read-only PR.
type UserSource interface {
	List(ctx context.Context) ([]UserRow, error)
	GetByLegacyID(ctx context.Context, id int) (UserRow, error)
}

// UserRow is the shim projection for a user. Only public fields are
// included — the translator does not emit sensitive ones in this PR.
type UserRow struct {
	LegacyID    int
	Slug        string
	Name        string
	Description string
	URL         string
	AvatarURL   string
}

// TermSource exposes the listing surface for taxonomy terms (categories
// and tags). The same interface backs both; the consumer (shim) decides
// the taxonomy name based on which route was hit.
type TermSource interface {
	List(ctx context.Context) ([]TermRow, error)
	GetByLegacyID(ctx context.Context, id int) (TermRow, error)
}

// TermRow is the shim projection for a taxonomy term.
type TermRow struct {
	LegacyID    int
	Slug        string
	Name        string
	Description string
	Count       int
	Parent      int
	Taxonomy    string // "category" or "post_tag"
}

// ErrNotFound is the canonical sentinel returned by all *Source
// GetByLegacyID methods when the id is unknown. The shim translates it
// to a 404 with a WP-shaped body.
var ErrNotFound = errors.New("wprest: not found")
