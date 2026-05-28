package posts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// HeaderPostPassword is the request header carrying the password for
// password-protected GETs. WordPress's classic UX uses a cookie; we
// prefer a header here because REST API callers don't manage cookies
// for the user-facing CMS browser flow — the cookie wiring layers on
// top of this header at the front-end.
const HeaderPostPassword = "X-Post-Password"

// HeaderVersion is the read-side companion to ETag. Clients echo it
// back as If-Match on writes. See the package doc for the rationale on
// splitting hash-based ETags from version-based optimistic concurrency.
const HeaderVersion = "X-Version"

// maxBodyBytes caps the request body for write paths. 1 MiB is enough
// for a deeply nested block tree plus metadata; anything bigger should
// flow through an upload endpoint, not this one.
const maxBodyBytes = 1 << 20

// Mount wires the posts/pages routes onto mux under the given base path.
// The pattern is:
//
//	posts.Mount(mux, "/api/v1/posts", Deps{..., PostType: posts.PostTypePost})
//	posts.Mount(mux, "/api/v1/pages", Deps{..., PostType: posts.PostTypePage})
//
// The base path is used verbatim — no trailing slash normalization, so
// the caller controls the canonical form. Returns an error rather than
// panicking when Deps is malformed so the server boot can surface it.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{
		store:      deps.Store,
		policy:     deps.Policy,
		audit:      deps.Audit,
		logger:     deps.Logger,
		postType:   deps.PostType,
		caps:       capsFor(deps.PostType),
		revalidate: deps.Revalidate,
	}

	mux.Handle("GET "+base, h.requireAuth(h.list))
	mux.Handle("GET "+base+"/{id}", h.requireAuth(h.get))
	mux.Handle("POST "+base, h.requireAuth(h.create))
	mux.Handle("PATCH "+base+"/{id}", h.requireAuth(h.update))
	mux.Handle("DELETE "+base+"/{id}", h.requireAuth(h.trash))
	return nil
}

// handlers carries the resolved dependencies for a single mount. One
// instance per call to Mount; no global state.
type handlers struct {
	store      Store
	policy     policy.Policy
	audit      *audit.Emitter
	logger     *slog.Logger
	postType   string
	caps       capabilitySet
	revalidate RevalidateNotifier
}

// requireAuth wraps a handler with the principal-presence guard.
// All endpoints in this package require an authenticated principal —
// read endpoints because we may need read_private_*, write endpoints
// because they require an author identity for audit + capability
// resolution. Anonymous read access is a future endpoint
// (/api/v1/feeds/...), not this one.
func (h *handlers) requireAuth(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// -----------------------------------------------------------------------------
// LIST
// -----------------------------------------------------------------------------

func (h *handlers) list(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	filter, err := parseListQuery(r)
	if err != nil {
		writeValidationError(w, err)
		return
	}

	// Reading "private" status requires the corresponding capability;
	// other statuses are visible to any authenticated principal at
	// list time. Object-level redaction (password-protected content)
	// happens at the per-row read path, not at list.
	if filter.Status == "private" {
		if d := h.policy.Can(pr, h.caps.readPrivate, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
	}

	rows, err := h.store.List(r.Context(), h.postType, filter)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "posts.list: store error", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list posts")
		return
	}

	// The memory store (and the production store) fetch limit+1 rows
	// so we know whether to surface a next_cursor. Truncate to the
	// requested page size before serializing.
	pageSize := filter.Limit
	if pageSize <= 0 {
		pageSize = DefaultListLimit
	}
	var next string
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		next = router.EncodeCursor(rows[len(rows)-1].ID)
	}

	page := router.Page[Post]{
		Data: rows,
		Pagination: router.PageInfo{
			NextCursor: next,
			// prev_cursor is reserved for future bidirectional pagination;
			// this issue's spec only requires next.
			PrevCursor: "",
		},
	}
	router.WriteJSON(w, http.StatusOK, page)
}

// parseListQuery extracts the filter from URL query params. Returns a
// [validation] error on malformed inputs so the handler surfaces a 400.
func parseListQuery(r *http.Request) (ListFilter, error) {
	q := r.URL.Query()
	var f ListFilter
	f.Status = q.Get("status")
	// `status=any` is a documented client convention meaning "all
	// statuses" — the admin posts page uses it to render drafts +
	// published in one list. Treat it as the absence of a status
	// filter rather than a validation error. Empty string is the same.
	if f.Status == "any" {
		f.Status = ""
	}
	if f.Status != "" {
		if _, ok := validStatuses[f.Status]; !ok {
			return f, validation{Code: "invalid_status", Detail: fmt.Sprintf("unknown status %q", f.Status)}
		}
	}
	f.AuthorID = q.Get("author")
	f.Search = q.Get("search")

	after := q.Get("after")
	if after != "" {
		decoded, err := router.ParseCursor(after)
		if err != nil {
			return f, validation{Code: "invalid_cursor", Detail: "after must be a valid cursor"}
		}
		f.After = decoded
	}

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return f, validation{Code: "invalid_limit", Detail: "limit must be a non-negative integer"}
		}
		if n == 0 {
			n = DefaultListLimit
		}
		if n > MaxListLimit {
			n = MaxListLimit
		}
		f.Limit = n
	} else {
		f.Limit = DefaultListLimit
	}
	return f, nil
}

// -----------------------------------------------------------------------------
// GET ONE
// -----------------------------------------------------------------------------

func (h *handlers) get(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}

	post, err := h.store.Get(r.Context(), h.postType, id)
	if err != nil {
		h.writeStoreError(w, r, err, "posts.get")
		return
	}

	// Private posts require the read_private_* capability. We check
	// after the load so we can return 404 for unknown ids without
	// leaking existence to unprivileged callers.
	if post.Status == "private" {
		if d := h.policy.Can(pr, h.caps.readPrivate, &post); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
	}

	// Conditional GET. The ETag is the strong form of
	// content_blocks_hash. If the client sent If-None-Match and it
	// matches, return 304 with no body — but always include the ETag
	// + X-Version headers so a client that lost them can recover.
	etag := router.HashETag(post.hash)
	router.SetETag(w, etag)
	w.Header().Set(HeaderVersion, strconv.Itoa(post.Version))

	if router.MatchesIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Password-protected posts: strip content unless the caller proved
	// access. The caller "proves access" by supplying the password in
	// the X-Post-Password header (byte-exact match against the stored
	// value — a future auth-layer issue will swap this for argon2id).
	if post.Protected && !passwordMatches(r, post) {
		post.ContentBlocks = json.RawMessage("[]")
	}
	router.WriteJSON(w, http.StatusOK, post)
}

func passwordMatches(r *http.Request, post Post) bool {
	if post.password == nil || *post.password == "" {
		return true
	}
	supplied := r.Header.Get(HeaderPostPassword)
	if supplied == "" {
		return false
	}
	// Byte-exact compare. See package doc for the planned argon2id
	// migration; the interface here doesn't change.
	return supplied == *post.password
}

// -----------------------------------------------------------------------------
// CREATE
// -----------------------------------------------------------------------------

func (h *handlers) create(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	// Route-level capability gate: must hold edit_posts / edit_pages.
	if d := h.policy.Can(pr, h.caps.edit, nil); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	var in CreateInput
	if err := decodeBody(r, &in); err != nil {
		writeValidationError(w, err)
		return
	}

	// Publishing a post requires the publish_* cap. We do this here
	// rather than inside the store so a denial doesn't side-effect.
	if in.Status != nil && *in.Status == "published" {
		if d := h.policy.Can(pr, h.caps.publish, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
	}

	if err := validateCreate(in); err != nil {
		writeValidationError(w, err)
		return
	}

	post, err := h.store.Create(r.Context(), h.postType, pr.UserID, in)
	if err != nil {
		h.writeStoreError(w, r, err, "posts.create")
		return
	}

	h.emitAudit(r.Context(), pr, post, "created")

	// ISR revalidation (#86). Created posts only need a revalidation
	// hook if they landed in "published" status — drafts and pending
	// rows are not visible to the public renderer.
	if post.Status == "published" {
		h.notifyRevalidate(r.Context(), post, "created")
	}

	w.Header().Set(HeaderVersion, strconv.Itoa(post.Version))
	router.SetETag(w, router.HashETag(post.hash))
	router.WriteJSON(w, http.StatusCreated, post)
}

// -----------------------------------------------------------------------------
// UPDATE
// -----------------------------------------------------------------------------

func (h *handlers) update(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}

	// Load first so we can authorize on author identity. Existence is
	// not leaked: an unauthenticated caller never reaches here (the
	// requireAuth wrapper returns 401), and authenticated callers see
	// 404 for unknown ids regardless of their cap set.
	existing, err := h.store.Get(r.Context(), h.postType, id)
	if err != nil {
		h.writeStoreError(w, r, err, "posts.update.load")
		return
	}

	// Object-level capability check: author of the row may edit with
	// the base cap; non-authors require edit_others_*.
	requiredCap := h.caps.edit
	if existing.AuthorID != pr.UserID {
		requiredCap = h.caps.editOthers
	}
	if d := h.policy.Can(pr, requiredCap, &existing); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	version, present, err := router.ParseIfMatchVersion(r)
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_if_match", "If-Match header is malformed")
		return
	}
	if !present {
		router.WriteError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match header is required")
		return
	}

	var in UpdateInput
	if err := decodeBody(r, &in); err != nil {
		writeValidationError(w, err)
		return
	}

	if in.Status != nil && *in.Status == "published" && existing.Status != "published" {
		// Publishing a previously-unpublished post — separate cap.
		if d := h.policy.Can(pr, h.caps.publish, &existing); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
	}

	if err := validateUpdate(in); err != nil {
		writeValidationError(w, err)
		return
	}

	updated, err := h.store.Update(r.Context(), h.postType, id, version, in)
	if err != nil {
		h.writeStoreError(w, r, err, "posts.update")
		return
	}

	h.emitAudit(r.Context(), pr, updated, "updated")

	// ISR revalidation (#86). Two cases trigger the hook:
	//   1. The row is still / now in "published" status — the public
	//      page needs a fresh render.
	//   2. The row WAS published and is no longer (unpublished /
	//      trashed via a status change) — the public page needs to
	//      transition to 404 / removed.
	if updated.Status == "published" || existing.Status == "published" {
		h.notifyRevalidate(r.Context(), updated, "updated")
	}

	w.Header().Set(HeaderVersion, strconv.Itoa(updated.Version))
	router.SetETag(w, router.HashETag(updated.hash))
	router.WriteJSON(w, http.StatusOK, updated)
}

// -----------------------------------------------------------------------------
// TRASH (soft delete)
// -----------------------------------------------------------------------------

func (h *handlers) trash(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}

	existing, err := h.store.Get(r.Context(), h.postType, id)
	if err != nil {
		h.writeStoreError(w, r, err, "posts.trash.load")
		return
	}

	requiredCap := h.caps.deleteOwn
	if existing.AuthorID != pr.UserID {
		requiredCap = h.caps.deleteOthers
	}
	if d := h.policy.Can(pr, requiredCap, &existing); !d.Allowed {
		router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
		return
	}

	version, present, err := router.ParseIfMatchVersion(r)
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_if_match", "If-Match header is malformed")
		return
	}
	if !present {
		router.WriteError(w, http.StatusPreconditionRequired, "if_match_required", "If-Match header is required")
		return
	}

	trashed, err := h.store.Trash(r.Context(), h.postType, id, version)
	if err != nil {
		h.writeStoreError(w, r, err, "posts.trash")
		return
	}

	h.emitAudit(r.Context(), pr, trashed, "trashed")

	w.Header().Set(HeaderVersion, strconv.Itoa(trashed.Version))
	router.WriteJSON(w, http.StatusOK, trashed)
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// decodeBody reads a JSON body into out. Enforces the maxBodyBytes cap
// and rejects unknown fields so client typos are surfaced rather than
// silently dropped. Returns a [validation] error on parse failure so
// the handler can surface a 400.
func decodeBody(r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return validation{Code: "missing_body", Detail: "request body is required"}
		}
		// http.MaxBytesReader-triggered failure surfaces as a "request
		// body too large" error string from net/http. Rather than
		// type-asserting, we inspect the message — net/http does not
		// export a sentinel.
		if strings.Contains(err.Error(), "too large") {
			return validation{Code: "body_too_large", Detail: "request body exceeds maximum size"}
		}
		return validation{Code: "invalid_body", Detail: "request body could not be parsed: " + err.Error()}
	}
	// Refuse trailing data — clients sometimes send array+object
	// concatenations by accident.
	if dec.More() {
		return validation{Code: "invalid_body", Detail: "request body must contain a single JSON value"}
	}
	return nil
}

// writeValidationError converts a [validation] to a 400 ProblemDetails.
// Other error shapes fall through to a 500 (defensive; callers should
// only invoke this for validation paths).
func writeValidationError(w http.ResponseWriter, err error) {
	if v, ok := asValidation(err); ok {
		router.WriteError(w, http.StatusBadRequest, v.Code, v.Detail)
		return
	}
	router.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
}

// writeStoreError maps store-layer sentinels to HTTP responses. Any
// other error is treated as an internal failure and logged with the
// supplied tag for triage.
func (h *handlers) writeStoreError(w http.ResponseWriter, r *http.Request, err error, tag string) {
	switch {
	case errors.Is(err, ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, ErrVersionConflict):
		router.WriteError(w, http.StatusPreconditionFailed, "version_mismatch", "If-Match version does not match current resource version")
	case errors.Is(err, ErrDuplicateSlug):
		router.WriteError(w, http.StatusConflict, "duplicate_slug", "a post with this slug already exists")
	default:
		h.logger.ErrorContext(r.Context(), tag+": store error", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// notifyRevalidate fires the ISR cache-invalidation hooks for a post
// or page that just published / unpublished. The renderer's URL
// convention is:
//
//	post type "post" → /posts/{slug}
//	post type "page" → /{slug}
//
// Plus the homepage feed ("/") for "post" — a new entry on the home
// list deserves a fresh render. Pages don't push to the home feed by
// default; they're typically reached via the main menu and the menu
// itself doesn't change on publish.
//
// All failures are logged and swallowed. A failed revalidation means
// the renderer serves a stale page for up to its next-revalidate
// interval, which is correct degrade behavior — failing the publish
// (rolling back the write) because Next.js was unreachable would be
// the wrong trade-off.
func (h *handlers) notifyRevalidate(ctx context.Context, post Post, verb string) {
	if h.revalidate == nil {
		return
	}
	var paths []string
	switch h.postType {
	case PostTypePost:
		if post.Slug != "" {
			paths = append(paths, "/posts/"+post.Slug)
		}
		// The home feed always wants a refresh when a post lands or
		// drops out — the published list is at "/", not "/posts".
		paths = append(paths, "/")
	case PostTypePage:
		if post.Slug != "" {
			paths = append(paths, "/"+post.Slug)
		}
	}
	if len(paths) == 0 {
		return
	}
	if err := h.revalidate.NotifyMany(ctx, paths); err != nil {
		// Best-effort: log at Warn (not Error) — staleness is a soft
		// failure, not a runbook-paging incident.
		h.logger.WarnContext(ctx, "posts: revalidate notify failed",
			slog.String("post_id", post.ID),
			slog.String("verb", verb),
			slog.String("post_type", h.postType),
			slog.Any("paths", paths),
			slog.Any("err", err),
		)
	}
}

// emitAudit is the audit emission shim. A nil emitter is tolerated.
// Errors are logged and swallowed — audit is best-effort, never the
// reason a user-facing write fails. (See packages/go/audit godoc.)
func (h *handlers) emitAudit(ctx context.Context, pr policy.Principal, post Post, verb string) {
	if h.audit == nil {
		return
	}
	em := h.audit.WithActor(pr.UserID)
	eventType := h.postType + "." + verb
	if err := em.Emit(ctx, eventType,
		audit.WithTarget(h.postType, post.ID),
		audit.WithMetadata(map[string]any{
			"version": post.Version,
			"status":  post.Status,
		}),
	); err != nil {
		h.logger.WarnContext(ctx, "posts: audit emit failed",
			slog.String("event", eventType),
			slog.String("post_id", post.ID),
			slog.Any("err", err),
		)
	}
}
