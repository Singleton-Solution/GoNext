package wprest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// maxWriteBodyBytes caps how much of a write request body we will read.
// 1 MiB is generous for a deeply nested post + metadata; anything larger
// is almost certainly a misuse (uploads go through a separate route).
// Live WP enforces a similar cap via PHP's post_max_size.
const maxWriteBodyBytes = 1 << 20

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

// Mount wires the WP REST v2 read+write shim onto mux at /wp-json/...
//
// Deps is required; nil fields cause a build-time wiring error rather
// than a per-request crash. The shim itself owns no state beyond the
// resolved Deps — every handler is a closure over the bundle.
//
// Write routes are registered when the corresponding *Sink dependency
// is wired. A nil sink falls through to a 405 handler with a WP-shaped
// body so a client that probes the surface gets `rest_no_route` rather
// than the net/http default plain-text 405.
func Mount(mux *http.ServeMux, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	// Production wiring hygiene: warn if a write sink is mounted
	// without policy/nonce verification. Tests intentionally skip
	// both to exercise the handler body in isolation.
	if (deps.PostsSink != nil || deps.PagesSink != nil ||
		deps.UsersSink != nil || deps.CategoriesSink != nil ||
		deps.TagsSink != nil) {
		if deps.NonceVerifier == nil {
			deps.Logger.Warn("wprest.Mount: write sink is wired but NonceVerifier is nil; writes will bypass nonce check")
		}
		if deps.Policy == nil {
			deps.Logger.Warn("wprest.Mount: write sink is wired but Policy is nil; capability checks are disabled")
		}
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

	// Write routes. The pattern across all resources is:
	//   POST   /<coll>          — create
	//   PUT    /<coll>/{id}     — full replace (we treat as upsert-patch
	//                             for WP compatibility; live WP does too)
	//   PATCH  /<coll>/{id}     — sparse update
	//   DELETE /<coll>/{id}     — delete (soft for users; sink-defined
	//                             for posts/terms)
	//
	// Where the sink is nil for a given resource, fall back to the
	// existing 405 refuseWrite handler so the response shape matches a
	// read-only deployment.
	registerWrites(mux, "posts", h.deps.PostsSink != nil, h.createPost, h.updatePost, h.deletePost, h.refuseWrite)
	registerWrites(mux, "pages", h.deps.PagesSink != nil, h.createPage, h.updatePage, h.deletePage, h.refuseWrite)
	registerWrites(mux, "users", h.deps.UsersSink != nil, h.createUser, h.updateUser, h.deleteUser, h.refuseWrite)
	registerWrites(mux, "categories", h.deps.CategoriesSink != nil, h.createCategory, h.updateCategory, h.deleteCategory, h.refuseWrite)
	registerWrites(mux, "tags", h.deps.TagsSink != nil, h.createTag, h.updateTag, h.deleteTag, h.refuseWrite)

	return nil
}

// registerWrites wires the POST/PUT/PATCH/DELETE table for a single
// collection base under /wp-json/wp/v2/. When wired is false (no sink
// for the resource), every method falls through to the refuse handler;
// otherwise the active handlers claim the natural verb-route pair and
// the nonsensical ones (e.g. PUT on the collection root, POST on the
// {id} sub-resource) still get the 405 fallback.
func registerWrites(mux *http.ServeMux, base string, wired bool,
	createH, updateH, deleteH, refuseH http.HandlerFunc) {

	if !wired {
		mux.HandleFunc("POST "+BasePath+"/wp/v2/"+base, refuseH)
		mux.HandleFunc("PUT "+BasePath+"/wp/v2/"+base, refuseH)
		mux.HandleFunc("PATCH "+BasePath+"/wp/v2/"+base, refuseH)
		mux.HandleFunc("DELETE "+BasePath+"/wp/v2/"+base, refuseH)
		mux.HandleFunc("POST "+BasePath+"/wp/v2/"+base+"/{id}", refuseH)
		mux.HandleFunc("PUT "+BasePath+"/wp/v2/"+base+"/{id}", refuseH)
		mux.HandleFunc("PATCH "+BasePath+"/wp/v2/"+base+"/{id}", refuseH)
		mux.HandleFunc("DELETE "+BasePath+"/wp/v2/"+base+"/{id}", refuseH)
		return
	}

	// Active write surface. Note PUT and PATCH both hit updateH — live
	// WP makes no semantic distinction between them on these routes
	// (they both call WP_REST_*_Controller::update_item).
	mux.HandleFunc("POST "+BasePath+"/wp/v2/"+base, createH)
	mux.HandleFunc("PUT "+BasePath+"/wp/v2/"+base+"/{id}", updateH)
	mux.HandleFunc("PATCH "+BasePath+"/wp/v2/"+base+"/{id}", updateH)
	mux.HandleFunc("DELETE "+BasePath+"/wp/v2/"+base+"/{id}", deleteH)

	// Nonsensical verbs still get a 405 fallback so the dispatch table
	// stays explicit.
	mux.HandleFunc("PUT "+BasePath+"/wp/v2/"+base, refuseH)
	mux.HandleFunc("PATCH "+BasePath+"/wp/v2/"+base, refuseH)
	mux.HandleFunc("DELETE "+BasePath+"/wp/v2/"+base, refuseH)
	mux.HandleFunc("POST "+BasePath+"/wp/v2/"+base+"/{id}", refuseH)
}

// readOnlyCollections is the list of collection segments that have
// 405-write registrations when no sink is wired. Kept in one place so
// adding a new resource is a one-line change.
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

// decodeWriteBody reads the JSON write body from r into out, applying
// the maxWriteBodyBytes cap. On parse failure it writes the WP-shaped
// error and returns false; the caller should bail.
//
// We deliberately permit unknown JSON fields (unlike the native rest
// layer's strict decoder) because WP REST clients often send plugin
// fields the shim doesn't model — silently ignoring them is the WP-
// compatible behavior. The sink layer is free to surface validation
// errors on the fields it does care about.
func decodeWriteBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, errCodeInvalidJSON,
			"Invalid JSON body passed.")
		return false
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxWriteBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, errCodeInvalidJSON,
				"Invalid JSON body passed.")
			return false
		}
		if strings.Contains(err.Error(), "too large") {
			writeError(w, http.StatusRequestEntityTooLarge, errCodeBodyTooLarge,
				"Request body exceeds the maximum allowed size.")
			return false
		}
		writeError(w, http.StatusBadRequest, errCodeInvalidJSON,
			"Invalid JSON body passed.")
		return false
	}
	// Trailing content (a second JSON value) is rejected — same
	// guardrail as the native rest layer to surface client misuse.
	if dec.More() {
		writeError(w, http.StatusBadRequest, errCodeInvalidJSON,
			"Request body must contain a single JSON value.")
		return false
	}
	return true
}

// parseIDFromPath extracts a positive integer id from r.PathValue("id").
// On failure, writes the WP-shaped error (the caller passes the right
// code per resource — `rest_post_invalid_id`, etc.) and returns 0,false.
func parseIDFromPath(w http.ResponseWriter, r *http.Request, errCode, errMsg string) (int, bool) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, errCode, errMsg)
		return 0, false
	}
	return id, true
}
