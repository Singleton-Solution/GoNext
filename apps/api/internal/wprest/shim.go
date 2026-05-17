package wprest

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// Base path under which the entire shim lives. Live WP installs serve
// these routes at `/wp-json/...` regardless of the underlying admin URL
// prefix; we follow that convention rather than the GoNext `/api/v1/...`
// scheme.
const BasePath = "/wp-json"

// defaultPerPage is the WP REST default for collection requests when the
// client omits `per_page`. Live WP returns 10; this number is part of
// the compatibility contract.
const defaultPerPage = 10

// maxPerPage is the upper bound for `per_page`. Live WP caps at 100; a
// request for more is silently clamped (same as core).
const maxPerPage = 100

// Mount wires the WP REST v2 read shim onto mux at /wp-json/...
//
// Deps is required; nil fields cause a build-time wiring error rather
// than a per-request crash. The shim itself owns no state beyond the
// resolved Deps — every handler is a closure over the bundle.
func Mount(mux *http.ServeMux, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{deps: deps}

	// Read routes. Each is a thin GET wrapper that calls the shared
	// listOrGet helper to dispatch on path.
	mux.HandleFunc("GET "+BasePath+"/wp/v2/posts", h.listPosts)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/posts/{id}", h.getPost)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/pages", h.listPages)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/pages/{id}", h.getPage)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/users", h.listUsers)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/users/{id}", h.getUser)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/categories", h.listCategories)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/categories/{id}", h.getCategory)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/tags", h.listTags)
	mux.HandleFunc("GET "+BasePath+"/wp/v2/tags/{id}", h.getTag)

	// Write methods on read collections: 405. Live WP returns
	// rest_no_route when an unsupported method is used. We register
	// explicit handlers (rather than relying on http.ServeMux's
	// 405 behavior) so we control the body shape — net/http's default
	// would emit a plain-text 405 that WP clients can't parse.
	for _, base := range readOnlyCollections {
		mux.HandleFunc("POST "+BasePath+"/wp/v2/"+base, h.refuseWrite)
		mux.HandleFunc("PUT "+BasePath+"/wp/v2/"+base, h.refuseWrite)
		mux.HandleFunc("PATCH "+BasePath+"/wp/v2/"+base, h.refuseWrite)
		mux.HandleFunc("DELETE "+BasePath+"/wp/v2/"+base, h.refuseWrite)
		mux.HandleFunc("POST "+BasePath+"/wp/v2/"+base+"/{id}", h.refuseWrite)
		mux.HandleFunc("PUT "+BasePath+"/wp/v2/"+base+"/{id}", h.refuseWrite)
		mux.HandleFunc("PATCH "+BasePath+"/wp/v2/"+base+"/{id}", h.refuseWrite)
		mux.HandleFunc("DELETE "+BasePath+"/wp/v2/"+base+"/{id}", h.refuseWrite)
	}

	return nil
}

// readOnlyCollections is the list of collection segments that have
// 405-write registrations. Kept in one place so adding a new resource is
// a one-line change.
var readOnlyCollections = []string{
	"posts", "pages", "users", "categories", "tags",
}

// handlers carries the resolved Deps for one Mount. One instance per
// call; no global state.
type handlers struct {
	deps Deps
}

// refuseWrite is the shared 405 handler for write methods on the
// read-only shim. The Allow header lists only GET — clients can sniff it
// to learn what methods this surface supports.
func (h *handlers) refuseWrite(w http.ResponseWriter, _ *http.Request) {
	writeMethodNotAllowed(w, "GET")
}

// -----------------------------------------------------------------------------
// Query parameter parsing
// -----------------------------------------------------------------------------

// wpQuery is the parsed form of the WP collection-query string. Every
// field is optional. Limits are clamped by parseWPQuery; Page is 1-indexed
// to match WP semantics (the native rest layer uses cursors instead, so
// we translate at the dispatch boundary).
type wpQuery struct {
	Page     int
	PerPage  int
	Search   string
	Embed    bool
	OrderBy  string // date|title|id|slug
	Order    string // asc|desc
	Cats     []int  // legacy_int_ids
	Tags     []int  // legacy_int_ids
	Slug     string
	Statuses []string
}

// parseWPQuery extracts a wpQuery from r. Returns an error string +
// boolean ok when an integer param is malformed; the caller writes a
// rest_invalid_param 400.
//
// The clamp semantics mirror live WP: out-of-range numeric params are
// silently clamped (rather than rejected) so simple typos don't break
// clients in production. Unknown orderby values fall back to "date".
func parseWPQuery(r *http.Request) (wpQuery, string, bool) {
	q := r.URL.Query()
	out := wpQuery{
		Page:    1,
		PerPage: defaultPerPage,
		OrderBy: "date",
		Order:   "desc",
	}

	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return out, "page", false
		}
		out.Page = n
	}
	if raw := q.Get("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return out, "per_page", false
		}
		if n > maxPerPage {
			n = maxPerPage
		}
		out.PerPage = n
	}

	out.Search = q.Get("search")
	out.Slug = q.Get("slug")

	// `_embed` is a presence flag in WP. Any value (including the
	// empty string from `?_embed`) means "yes" — we honor that exactly.
	if _, ok := q["_embed"]; ok {
		out.Embed = true
	}

	if ob := q.Get("orderby"); ob != "" {
		switch ob {
		case "date", "title", "id", "slug", "modified":
			out.OrderBy = ob
		default:
			// Unknown orderby → fall back to date. Live WP returns 400
			// here, but the spec-compatible-but-permissive choice
			// reduces migration noise; we log instead.
			out.OrderBy = "date"
		}
	}
	if ord := q.Get("order"); ord != "" {
		switch strings.ToLower(ord) {
		case "asc", "desc":
			out.Order = strings.ToLower(ord)
		default:
			return out, "order", false
		}
	}

	// `categories` and `tags` accept either CSV (`categories=1,2,3`) or
	// the PHP array form (`categories[]=1&categories[]=2`). Both are
	// commonly seen in the wild; we handle both transparently.
	out.Cats = parseIntList(q, "categories")
	out.Tags = parseIntList(q, "tags")

	// `status` can be repeated (`status=publish&status=draft`) or a
	// single CSV. Reads through the shim are public-content-only in
	// this PR, so any caller-provided status is overlaid on top of the
	// default "publish" filter inside listPosts.
	if vs := q["status"]; len(vs) > 0 {
		for _, v := range vs {
			for _, part := range strings.Split(v, ",") {
				p := strings.TrimSpace(part)
				if p != "" {
					out.Statuses = append(out.Statuses, p)
				}
			}
		}
	}
	return out, "", true
}

// parseIntList parses a query key in both CSV and PHP-array forms. The
// PHP form is `key[]=1&key[]=2`; net/url surfaces that as q["key[]"]
// rather than q["key"], so we look at both keys. Empty inputs return nil
// (not an empty slice) so callers can distinguish "client didn't ask"
// from "client asked for zero terms" — the latter is a valid filter that
// should match nothing.
func parseIntList(q map[string][]string, key string) []int {
	var raw []string
	if vs := q[key]; len(vs) > 0 {
		raw = append(raw, vs...)
	}
	if vs := q[key+"[]"]; len(vs) > 0 {
		raw = append(raw, vs...)
	}
	if len(raw) == 0 {
		return nil
	}
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		for _, part := range strings.Split(v, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				// Skip non-numeric IDs — live WP behavior is to
				// silently drop them rather than 400 the request.
				continue
			}
			out = append(out, n)
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// Pagination
// -----------------------------------------------------------------------------

// writePaginationHeaders sets the WP-standard headers and computes
// TotalPages. WP clients (notably the @wordpress/api-fetch JS package)
// depend on both headers being present on every collection response.
func writePaginationHeaders(w http.ResponseWriter, total, perPage int) {
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	pages := total / perPage
	if total%perPage != 0 {
		pages++
	}
	if pages < 1 {
		pages = 1
	}
	w.Header().Set("X-WP-Total", strconv.Itoa(total))
	w.Header().Set("X-WP-TotalPages", strconv.Itoa(pages))
}

// applyPagination slices items per the 1-indexed page + per_page params,
// returning the page's slice. Returns nil when page is out of range —
// callers emit an empty array, not a 404 (matches WP).
func applyPagination[T any](items []T, page, perPage int) []T {
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * perPage
	if start >= len(items) {
		return nil
	}
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// -----------------------------------------------------------------------------
// Body writer
// -----------------------------------------------------------------------------

// contextDone reports whether the request context is already canceled.
// We check this at the top of each list handler so we don't waste CPU on
// a doomed response.
func contextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
