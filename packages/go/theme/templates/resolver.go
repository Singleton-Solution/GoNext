package templates

// extensions is the ordered list of filename extensions the resolver
// tries for each precedence-list candidate. ".tsx" wins because
// GoNext themes are React component packages (docs §1); ".html" is
// the classic-theme fallback for themes ported from PHP or for
// static-site exports.
//
// The list is intentionally closed: adding ".mdx" or ".jsx" would
// expand the contract and is reserved for an explicit follow-up.
var extensions = []string{".tsx", ".html"}

// DefaultResolver walks the canonical WordPress template hierarchy
// described in docs/03-theme-system.md §4.2. It holds no state, so a
// single value can be shared across goroutines; callers that want a
// custom precedence list should implement Resolver directly rather
// than embedding this struct.
type DefaultResolver struct{}

// NewDefaultResolver returns the stock resolver. It exists as a
// constructor so the call site reads naturally
// (`templates.NewDefaultResolver()`) and so future configuration
// knobs (e.g. an extension allowlist) can be added without breaking
// existing callers.
func NewDefaultResolver() *DefaultResolver {
	return &DefaultResolver{}
}

// Resolve implements Resolver. It builds the precedence list for the
// request's Type, then walks it in order, returning the first
// filename ThemeFiles.Has reports present. Each base name is tried
// with the ".tsx" extension first and ".html" second; the first hit
// wins outright, so a theme that ships both single-book.tsx and
// single-book.html resolves to the .tsx file.
//
// Returns ErrUnknownRequestType when req.Type is unset, and
// ErrNoIndex when the theme ships neither index.tsx nor index.html.
// Both errors signal a defect (in the caller or in the theme
// package) rather than a transient problem, and should be surfaced
// to humans rather than retried.
func (DefaultResolver) Resolve(req Request, files ThemeFiles) (string, error) {
	if files == nil {
		return "", ErrNoIndex
	}

	candidates := buildCandidates(req)
	if candidates == nil {
		return "", ErrUnknownRequestType
	}

	for _, base := range candidates {
		if base == "" {
			continue
		}
		for _, ext := range extensions {
			name := base + ext
			if files.Has(name) {
				return name, nil
			}
		}
	}

	// The hierarchy always ends with "index", so reaching this point
	// means index.tsx and index.html are both missing. Per §4.1 that
	// is a theme-packaging defect: the index template is mandatory.
	return "", ErrNoIndex
}

// buildCandidates returns the ordered base-name list for a Request,
// least-suffix to most-suffix elided so the first entry is the
// most-specific match. Callers add ".tsx" / ".html" themselves.
//
// Returning nil (not an empty slice) signals "I don't recognise this
// RequestType"; the empty-slice case is reserved for "I recognise it
// but there are no candidates" which never actually happens — every
// recognised type ends with "index".
//
// Each branch is kept small and explicit rather than table-driven so
// the precedence reads top-to-bottom against the docs without a
// reader having to mentally execute a template-method pattern.
func buildCandidates(req Request) []string {
	switch req.Type {
	case RequestTypeSingular:
		return singularCandidates(req)
	case RequestTypeArchive:
		return archiveCandidates(req)
	case RequestTypeTaxonomy:
		return taxonomyCandidates(req)
	case RequestTypeAuthor:
		return authorCandidates(req)
	case RequestTypeDate:
		return []string{"date", "archive", "index"}
	case RequestTypeSearch:
		return []string{"search", "index"}
	case RequestTypeHome:
		return []string{"home", "index"}
	case RequestTypeFrontPage:
		return []string{"front-page", "home", "index"}
	case RequestTypeNotFound:
		return []string{"404", "index"}
	default:
		return nil
	}
}

// singularCandidates returns the precedence list for a single post /
// page / CPT view. The order is:
//
//	single-{postType}-{slug}     most specific, only when slug set
//	single-{postType}-{id}       fallback when only id is known
//	single-{postType}            any item of that post type
//	single                       any single item (classic alias)
//	singular                     any single item (modern name)
//	index                        ultimate fallback
//
// "single" and "singular" are siblings rather than parent/child in
// the spec; we emit "single" before "singular" to match the order
// the task spec writes them in, which matches the classic-WP
// expectation that a theme shipping single.tsx wants it picked over
// singular.tsx.
func singularCandidates(req Request) []string {
	out := make([]string, 0, 6)
	if req.PostType != "" {
		if req.PostSlug != "" {
			out = append(out, "single-"+req.PostType+"-"+req.PostSlug)
		}
		if req.PostID != "" {
			out = append(out, "single-"+req.PostType+"-"+req.PostID)
		}
		out = append(out, "single-"+req.PostType)
	}
	out = append(out, "single", "singular", "index")
	return out
}

// archiveCandidates returns the precedence list for a post-type
// archive (e.g. /books):
//
//	archive-{postType}
//	archive
//	index
//
// If PostType is empty the post-type-specific entry is skipped so a
// caller that wants the bare /archive page can pass Type=Archive
// with no PostType set.
func archiveCandidates(req Request) []string {
	out := make([]string, 0, 3)
	if req.PostType != "" {
		out = append(out, "archive-"+req.PostType)
	}
	out = append(out, "archive", "index")
	return out
}

// taxonomyCandidates returns the precedence list for a taxonomy term
// archive (e.g. /genre/cookbooks):
//
//	taxonomy-{tax}-{term}
//	taxonomy-{tax}
//	taxonomy
//	archive
//	index
//
// Built-in taxonomies (category, post_tag) have additional friendlier
// aliases per §4.2; those are reserved for a follow-up so the MVP
// surface stays minimal.
func taxonomyCandidates(req Request) []string {
	out := make([]string, 0, 5)
	if req.TaxonomySlug != "" {
		if req.TermSlug != "" {
			out = append(out, "taxonomy-"+req.TaxonomySlug+"-"+req.TermSlug)
		}
		out = append(out, "taxonomy-"+req.TaxonomySlug)
	}
	out = append(out, "taxonomy", "archive", "index")
	return out
}

// authorCandidates returns the precedence list for an author
// archive. The docs name two id-suffixed forms — author-{username}
// and author-{id} — with the username variant winning. We expose the
// username via Request.PostSlug because there is no dedicated
// AuthorHandle field; a future-friendlier signature is reserved for
// when the user table lands.
//
//	author-{id}
//	author-{username}
//	author
//	archive
//	index
//
// The id-first order matches the task spec ("author-42.tsx →
// author-<handle>.tsx") which mirrors the convention that a numeric
// permalink is always more specific than the slugged one for a
// given user.
func authorCandidates(req Request) []string {
	out := make([]string, 0, 5)
	if req.AuthorID != "" {
		out = append(out, "author-"+req.AuthorID)
	}
	if req.PostSlug != "" {
		out = append(out, "author-"+req.PostSlug)
	}
	out = append(out, "author", "archive", "index")
	return out
}
