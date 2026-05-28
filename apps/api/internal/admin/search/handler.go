package search

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	pkgsearch "github.com/Singleton-Solution/GoNext/packages/go/search"
	"github.com/Singleton-Solution/GoNext/packages/go/util/queryparse"
)

// validSearchStatuses is the closed set the admin search endpoint
// accepts for ?status=. Matches the post_status enum in
// 000001_init.up.sql — searchable rows live in the posts table, so
// the valid filter values are the same ones REST /api/v1/posts
// accepts. queryparse.ParseStatus also recognises "" and "any" as
// "no filter" before consulting this set.
var validSearchStatuses = map[string]struct{}{
	"draft":     {},
	"pending":   {},
	"published": {},
	"scheduled": {},
	"private":   {},
	"trash":     {},
}

// Searcher is the read-only contract the handler needs. The
// concrete *pkgsearch.Store satisfies it. Keeping the interface
// local lets unit tests stub one method without bringing the SQL
// machinery up.
type Searcher interface {
	Search(ctx context.Context, q string, opts pkgsearch.SearchOpts) (*pkgsearch.Results, error)
}

// Handler is the HTTP entry point for the admin search endpoint.
// Constructed once at boot; safe for concurrent use.
type Handler struct {
	search Searcher
	logger *slog.Logger
}

// NewHandler returns an admin search handler. Panics on nil search
// so a wiring mistake fails at boot.
func NewHandler(s Searcher, logger *slog.Logger) *Handler {
	if s == nil {
		panic("admin/search: searcher is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		search: s,
		logger: logger.With(slog.String("component", "admin.search")),
	}
}

// ServeHTTP implements http.Handler. The URL contract:
//
//	GET /api/v1/admin/search?q=<term>&types=post,page&limit=20&offset=0
//
// The principal must be present (the Mount-time middleware enforces
// auth) — we don't re-check here.
//
// 400 — q is empty or whitespace-only.
// 405 — method other than GET.
// 500 — search.Search returned a non-empty-query error.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		router.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		router.WriteError(w, http.StatusBadRequest, "empty_query", "q is required")
		return
	}

	opts, err := parseOpts(r)
	if err != nil {
		// parseOpts only fails on invalid_status today; route everything
		// through a single branch so future validations can extend
		// parseOpts without growing the handler.
		if errors.Is(err, queryparse.ErrInvalidStatus) {
			router.WriteError(w, http.StatusBadRequest, "invalid_status",
				"status must be one of draft, pending, published, scheduled, private, trash (or omitted / 'any')")
			return
		}
		router.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	res, err := h.search.Search(r.Context(), q, opts)
	if err != nil {
		if errors.Is(err, pkgsearch.ErrEmptyQuery) {
			// Defensive — parseOpts already filtered, but if a future
			// caller bypasses it, surface a clean 400.
			router.WriteError(w, http.StatusBadRequest, "empty_query", "q is required")
			return
		}
		h.logger.ErrorContext(r.Context(), "admin.search: store error",
			slog.String("err", err.Error()),
			slog.String("q", q))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}
	router.WriteJSON(w, http.StatusOK, res)
}

// parseOpts maps URL query params onto a SearchOpts. The admin
// surface deliberately does NOT default-filter by status; callers
// who want only published rows pass &status=published explicitly.
// The "any" alias and the empty string both mean "no filter"; an
// unknown status returns queryparse.ErrInvalidStatus so the handler
// surfaces a 400 instead of slipping it into the SQL parameter and
// returning empty results.
func parseOpts(r *http.Request) (pkgsearch.SearchOpts, error) {
	q := r.URL.Query()
	status, err := queryparse.ParseStatus(q.Get("status"), validSearchStatuses)
	if err != nil {
		return pkgsearch.SearchOpts{}, err
	}
	opts := pkgsearch.SearchOpts{
		Status: status,
	}
	if t := q.Get("types"); t != "" {
		// Split on comma so the typical "?types=post,page" works.
		// Whitespace around each entry is tolerated; the package
		// normalises further (lowercase, dedupe).
		for _, raw := range strings.Split(t, ",") {
			trimmed := strings.TrimSpace(raw)
			if trimmed != "" {
				opts.Types = append(opts.Types, trimmed)
			}
		}
	}
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	if raw := q.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			opts.Offset = n
		}
	}
	return opts, nil
}

// Mount registers the handler at base + "/search" behind the
// authentication gate. The route is allowed for any logged-in
// principal — there is no narrower cap, by design. See doc.go.
//
// base is the admin REST root, typically "/api/v1/admin".
func Mount(mux *http.ServeMux, base string, pol policy.Policy, h *Handler) error {
	if pol == nil {
		return errors.New("admin/search.Mount: policy is required")
	}
	if h == nil {
		return errors.New("admin/search.Mount: handler is required")
	}
	mux.Handle("GET "+base+"/search", requirePrincipal(h))
	return nil
}

// requirePrincipal is a tiny gate equivalent to policy.Require but
// without a capability check: any authenticated principal is
// allowed through. The admin sidebar's cmd+k search box needs to
// work for every signed-in role.
func requirePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := policy.FromContext(r.Context()); !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
