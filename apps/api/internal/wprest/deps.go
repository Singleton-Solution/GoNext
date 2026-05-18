package wprest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Deps is the dependency bundle passed to [Mount].
//
// The shim is built as a translation layer on top of small read-only
// "source" interfaces — one per resource type — rather than pulling in
// the native rest/posts package directly. The seams keep the shim
// testable with simple fakes, and they let production wire production
// stores while tests substitute deterministic in-memory ones.
//
// The write surface (POST/PUT/PATCH/DELETE) layers on top of the same
// source interfaces with parallel *Sink interfaces. The sinks are
// optional: a nil sink for a resource means "writes are disabled on
// this resource and return 405". This lets a deployment expose the
// posts-read + posts-write surface but, e.g., not the users-write
// surface, by simply not wiring the UserSink.
type Deps struct {
	// Posts sources `post` rows for /wp-json/wp/v2/posts.
	Posts PostSource

	// PostsSink is the write surface for /wp-json/wp/v2/posts. May be nil;
	// when nil, write methods on /posts return the shim's standard 405.
	PostsSink PostSink

	// Pages sources `page` rows for /wp-json/wp/v2/pages. Pages and
	// posts have identical shape; the discriminator is the source.
	Pages PostSource

	// PagesSink is the write surface for /wp-json/wp/v2/pages.
	PagesSink PostSink

	// Users sources user rows for /wp-json/wp/v2/users. May be nil; if
	// nil, the users routes return an empty collection (live WP behavior
	// when the public-users surface is disabled).
	Users UserSource

	// UsersSink is the write surface for /wp-json/wp/v2/users. May be
	// nil; when nil, writes return 405. Note: WP semantics for user
	// DELETE are "deactivate" (status flip), NOT "drop the row" —
	// implementations should soft-delete.
	UsersSink UserSink

	// Categories sources category terms for /wp-json/wp/v2/categories.
	// May be nil → empty collection.
	Categories TermSource

	// CategoriesSink is the write surface for /wp-json/wp/v2/categories.
	CategoriesSink TermSink

	// Tags sources tag terms for /wp-json/wp/v2/tags. May be nil →
	// empty collection.
	Tags TermSource

	// TagsSink is the write surface for /wp-json/wp/v2/tags.
	TagsSink TermSink

	// Policy resolves capability questions on write requests. May be
	// nil in test mounts that only exercise reads; nil disables the
	// capability gate, which is NOT safe in production. Mount logs a
	// warning when any sink is wired but Policy is nil.
	Policy policy.Policy

	// PrincipalFromContext, when set, overrides the default
	// policy.FromContext lookup for the request principal. Lets a host
	// app plumb a different context key without forking the shim.
	PrincipalFromContext func(context.Context) (policy.Principal, bool)

	// NonceVerifier validates the X-WP-Nonce header on writes. May be
	// nil; nil means "do not verify" — appropriate for tests but a
	// production wiring bug. Mount logs a warning when any sink is
	// wired but NonceVerifier is nil.
	NonceVerifier NonceVerifier

	// Audit is the audit emitter for write events. Best-effort: a nil
	// emitter or an emit error never breaks the user-facing write.
	Audit *audit.Emitter

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

// ErrInvalidTerm is returned by a PostSink when an in-payload categories[]
// or tags[] reference does not resolve to a known term. The shim
// translates it to a 400 `rest_term_invalid` so the WP error code matches
// what live WP emits for the same condition.
var ErrInvalidTerm = errors.New("wprest: invalid term reference")

// ErrInvalidInput is returned by sinks for any validation failure that
// doesn't fit a more specific sentinel — empty title where required,
// unsupported status transition, malformed slug, etc. The shim maps it
// to a 400 `rest_invalid_param`. Sinks should prefer the more specific
// sentinel (ErrInvalidTerm) when applicable; this is the catch-all.
var ErrInvalidInput = errors.New("wprest: invalid input")

// ErrDuplicate is returned when a uniqueness constraint is violated
// (slug already taken, username already exists). Maps to 409
// `rest_<resource>_exists`.
var ErrDuplicate = errors.New("wprest: duplicate")

// PostSink is the write counterpart to PostSource. The same interface
// backs posts and pages; the sink implementation handles the post_type
// translation at its boundary.
//
// Each method takes the same WP-shaped input the translator produces.
// The sink is responsible for resolving WP-side ids (legacy_int_id) to
// the native UUIDs, applying any further validation, and returning the
// resulting row in WP-shaped PostRow form. The shim layer above never
// sees the native id at all.
type PostSink interface {
	// Create inserts a new row and returns the resulting PostRow. The
	// returned row's LegacyID, Date, DateGMT, Modified, ModifiedGMT
	// must be populated.
	Create(ctx context.Context, actorUserID string, in PostWriteInput) (PostRow, error)

	// Update applies a sparse update by legacy_int_id. Fields set to
	// nil pointers (or zero-length slices for the slice fields) are
	// not modified. Returns the updated row.
	Update(ctx context.Context, actorUserID string, legacyID int, in PostWriteInput) (PostRow, error)

	// Delete soft-deletes the row at legacy_int_id and returns the
	// row's state immediately before deletion. The shim emits the WP-
	// shaped `{deleted: true, previous: {...}}` body, so the "previous"
	// envelope must be a faithful snapshot.
	Delete(ctx context.Context, actorUserID string, legacyID int) (PostRow, error)
}

// PostWriteInput is the WP-translated write payload. Pointer fields
// distinguish "client omitted" from "client sent zero value" so a
// sparse PATCH leaves untouched columns alone.
//
// The translator in posts.go converts the WP request body (title.raw,
// content.raw, status string, categories[]) into this shape. Sinks
// implement against this, not the WP envelope, so their unit tests
// don't have to construct full HTTP requests.
type PostWriteInput struct {
	// Type is "post" or "page". The shim sets it from the route; sinks
	// MUST honor it (never let a /posts call create a page row).
	Type string

	// Slug, Title, ContentHTML, ExcerptHTML, Status, Format,
	// CommentStatus, PingStatus, Template are the WP scalars. ContentHTML
	// is the raw HTML; the sink (or its store) is responsible for
	// converting to blocks via html2blocks.
	Slug          *string
	Title         *string
	ContentHTML   *string
	ExcerptHTML   *string
	Status        *string
	Format        *string
	CommentStatus *string
	PingStatus    *string
	Template      *string

	// AuthorID is the WP-side legacy author id. nil = "do not change".
	AuthorID *int

	// FeaturedMedia is the WP-side legacy media id. nil = no change.
	FeaturedMedia *int

	// Sticky maps to the WP "stick to front" flag.
	Sticky *bool

	// Categories / Tags are the WP-side legacy term ids. nil = no
	// change; a non-nil (possibly empty) slice replaces the set.
	Categories *[]int
	Tags       *[]int

	// Date / DateGMT let clients backdate or schedule. nil = let the
	// sink decide (typically "now" for creates).
	Date    *time.Time
	DateGMT *time.Time

	// Password sets a per-post password. nil = no change; empty string
	// clears the password.
	Password *string

	// Meta is reserved for plugin fields. The shim does not interpret
	// it; sinks may persist arbitrary key/value pairs.
	Meta map[string]any
}

// UserSink is the write counterpart to UserSource. WP semantics for
// users:
//
//   - POST creates a user. The body carries username / email / password
//     / roles[]; the slug is normally derived from username.
//   - PUT/PATCH updates fields.
//   - DELETE deactivates (status flip, NOT a row drop). Live WP allows
//     a `reassign` query param to move the user's content to another
//     id; we accept it but it's the sink's job to implement.
type UserSink interface {
	Create(ctx context.Context, actorUserID string, in UserWriteInput) (UserRow, error)
	Update(ctx context.Context, actorUserID string, legacyID int, in UserWriteInput) (UserRow, error)
	Delete(ctx context.Context, actorUserID string, legacyID int, reassignTo *int) (UserRow, error)
}

// UserWriteInput is the WP-translated user write payload.
type UserWriteInput struct {
	Username    *string
	Email       *string
	Password    *string
	Name        *string
	Slug        *string
	URL         *string
	Description *string
	// Roles is the WP-side role slugs. Sinks translate to native roles
	// (the policy.Role values) and apply any cap-mapping checks.
	Roles *[]string
}

// TermSink is the write counterpart to TermSource. Backs both
// categories and tags; the taxonomy discriminator comes from the route.
type TermSink interface {
	Create(ctx context.Context, actorUserID, taxonomy string, in TermWriteInput) (TermRow, error)
	Update(ctx context.Context, actorUserID, taxonomy string, legacyID int, in TermWriteInput) (TermRow, error)
	Delete(ctx context.Context, actorUserID, taxonomy string, legacyID int) (TermRow, error)
}

// TermWriteInput is the WP-translated term write payload.
type TermWriteInput struct {
	Name        *string
	Slug        *string
	Description *string
	Parent      *int
}
