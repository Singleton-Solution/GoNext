package templates

import "errors"

// RequestType enumerates the query buckets the router classifies an
// incoming HTTP request into. The names mirror the precedence-list
// headings in docs/03-theme-system.md §4.2; each value selects one of
// the canonical hierarchies DefaultResolver walks.
type RequestType int

const (
	// RequestTypeUnknown is the zero value. Resolve treats it as an
	// invalid request rather than silently defaulting to one of the
	// real buckets — a caller that forgets to set Type should hear
	// about it immediately.
	RequestTypeUnknown RequestType = iota

	// RequestTypeSingular is a single post / page / CPT view. The
	// precedence list uses PostType + PostSlug / PostID:
	//   single-{postType}-{slug}.tsx
	//   single-{postType}.tsx
	//   single.tsx
	//   singular.tsx
	//   index.tsx
	RequestTypeSingular

	// RequestTypeArchive is a post-type archive (e.g. /books):
	//   archive-{postType}.tsx
	//   archive.tsx
	//   index.tsx
	RequestTypeArchive

	// RequestTypeTaxonomy is a taxonomy term archive (e.g.
	// /genre/cookbooks):
	//   taxonomy-{tax}-{term}.tsx
	//   taxonomy-{tax}.tsx
	//   taxonomy.tsx
	//   archive.tsx
	//   index.tsx
	RequestTypeTaxonomy

	// RequestTypeAuthor is an author archive. The precedence list
	// keys off both AuthorID (numeric) and a human-readable handle
	// the caller may pass via PostSlug — the docs spell this
	// "author-{username}" / "author-{id}".
	RequestTypeAuthor

	// RequestTypeDate is a date archive. The MVP precedence is the
	// short form date.tsx → archive.tsx → index.tsx; year/month/day
	// granularity is left to a follow-up issue (see §4.2).
	RequestTypeDate

	// RequestTypeSearch is a search-result page:
	//   search.tsx
	//   index.tsx
	RequestTypeSearch

	// RequestTypeHome is the blog-home (posts) page when a static
	// front page is configured:
	//   home.tsx
	//   index.tsx
	RequestTypeHome

	// RequestTypeFrontPage is the site root when the front page is a
	// dedicated landing template:
	//   front-page.tsx
	//   home.tsx
	//   index.tsx
	RequestTypeFrontPage

	// RequestTypeNotFound is the 404 page:
	//   404.tsx
	//   index.tsx
	RequestTypeNotFound
)

// String returns the stable lowercase identifier for a RequestType,
// matching the strings used in the docs and in test fixtures.
func (r RequestType) String() string {
	switch r {
	case RequestTypeSingular:
		return "singular"
	case RequestTypeArchive:
		return "archive"
	case RequestTypeTaxonomy:
		return "taxonomy"
	case RequestTypeAuthor:
		return "author"
	case RequestTypeDate:
		return "date"
	case RequestTypeSearch:
		return "search"
	case RequestTypeHome:
		return "home"
	case RequestTypeFrontPage:
		return "front-page"
	case RequestTypeNotFound:
		return "404"
	default:
		return "unknown"
	}
}

// Request is the resolver's input: a fully-classified query. The
// router populates the relevant fields for each RequestType and
// leaves the rest at the zero value. Fields that don't apply to a
// given Type are ignored by Resolve — e.g. TaxonomySlug is irrelevant
// for a Singular request.
type Request struct {
	// Type is the precedence-list selector. Required.
	Type RequestType

	// PostType is the slug of the post type for Singular / Archive
	// requests (e.g. "post", "page", "book").
	PostType string

	// PostSlug is the slug of the requested item for Singular
	// requests, OR the human-readable author handle for Author
	// requests (the docs call this "author-{username}").
	PostSlug string

	// PostID is the stringified numeric ID for Singular requests,
	// kept as a string so callers don't have to decide between
	// int / int64 / uuid representations here.
	PostID string

	// TaxonomySlug is the taxonomy slug for Taxonomy requests
	// (e.g. "genre", "category", "post_tag").
	TaxonomySlug string

	// TermSlug is the term slug for Taxonomy requests
	// (e.g. "cookbooks").
	TermSlug string

	// TermID is the stringified numeric term ID for Taxonomy
	// requests. Currently unused by DefaultResolver's hierarchy but
	// reserved so the surface doesn't have to break when the
	// id-suffixed candidate is wired in.
	TermID string

	// AuthorID is the stringified numeric author ID for Author
	// requests (e.g. "42" → "author-42.tsx").
	AuthorID string

	// IsFront marks the site root. The router sets this in tandem
	// with Type=RequestTypeFrontPage; it's separate so a caller that
	// wants to introspect "is this request the home page?" without
	// touching Type can do so.
	IsFront bool

	// IsHome marks the blog-home (posts) page. Set in tandem with
	// Type=RequestTypeHome.
	IsHome bool

	// Is404 marks a not-found response. Set in tandem with
	// Type=RequestTypeNotFound.
	Is404 bool
}

// ThemeFiles is the single abstraction the resolver needs over the
// theme's file backing store. Production code wraps an os.DirFS scan
// of the active theme directory; tests use an in-memory implementation
// (see resolver_test.go's mapFiles); a future block-theme backend can
// wrap a database lookup. All without changing this package.
//
// Has must return true if and only if the named template is present
// and renderable. The filename is the bare basename — "single.tsx",
// "index.html" — with no directory prefix. Callers that want to scope
// by subdirectory should build that into the underlying
// implementation; the resolver itself is path-agnostic.
type ThemeFiles interface {
	Has(filename string) bool
}

// Resolver picks the most-specific template a theme ships for a given
// request, walking the canonical WordPress hierarchy. A separate
// interface exists (rather than a bare function) so plugins or future
// experiments can swap in a custom precedence list without changing
// the call sites.
type Resolver interface {
	// Resolve returns the basename of the chosen template. The
	// returned name is suitable for handing back to ThemeFiles.Has
	// — same alphabet, same extensions. If even index.tsx /
	// index.html is missing, Resolve returns ErrNoIndex; that
	// signals a malformed theme, not a transient error.
	Resolve(req Request, files ThemeFiles) (string, error)
}

// ErrNoIndex is returned by Resolve when the theme is missing the
// ultimate fallback ("index.tsx" or "index.html"). Per
// docs/03-theme-system.md §4.1 every theme MUST ship index, so this
// is treated as a theme-packaging defect: callers should surface it
// to the admin or fail the install rather than silently swallow it.
var ErrNoIndex = errors.New("templates: theme is missing index.tsx/index.html — themes must ship index as the ultimate fallback")

// ErrUnknownRequestType is returned by Resolve when called with a
// Request whose Type is RequestTypeUnknown (or any unrecognised
// future value). The router is expected to classify every request
// before calling the resolver, so this is a programmer error.
var ErrUnknownRequestType = errors.New("templates: request has unknown type — router must set Request.Type")
