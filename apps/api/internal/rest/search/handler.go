package search

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	pkgsearch "github.com/Singleton-Solution/GoNext/packages/go/search"
)

// Searcher is the local read-only contract the handler depends on.
// Identical shape to the one in the admin/search package; we don't
// share the type because import-direction would force a needless
// dependency from the public route to the admin package.
type Searcher interface {
	Search(ctx context.Context, q string, opts pkgsearch.SearchOpts) (*pkgsearch.Results, error)
}

// publicStatus is the only status this endpoint will surface. Hard-
// coded; the URL parameter is ignored if present. See doc.go.
const publicStatus = "published"

// Handler is the HTTP entry point for the public search endpoint.
// Constructed once at boot and reused; thread-safe.
type Handler struct {
	search Searcher
	logger *slog.Logger
}

// NewHandler returns a public search handler. Panics on nil
// searcher so a wiring mistake fails at boot.
func NewHandler(s Searcher, logger *slog.Logger) *Handler {
	if s == nil {
		panic("rest/search: searcher is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		search: s,
		logger: logger.With(slog.String("component", "rest.search")),
	}
}

// ServeHTTP implements http.Handler.
//
// Inputs:
//
//	?q       — required, the search term.
//	?types   — optional, comma-separated allowlist (post, page).
//	?limit   — optional, page size.
//	?offset  — optional, pagination cursor.
//	?total   — optional truthy value to compute Total.
//
// Outputs: search.Results JSON body.
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

	opts := parsePublicOpts(r)

	res, err := h.search.Search(r.Context(), q, opts)
	if err != nil {
		if errors.Is(err, pkgsearch.ErrEmptyQuery) {
			router.WriteError(w, http.StatusBadRequest, "empty_query", "q is required")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest.search: store error",
			slog.String("err", err.Error()))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "search failed")
		return
	}
	router.WriteJSON(w, http.StatusOK, res)
}

// parsePublicOpts maps URL query params onto a SearchOpts. The
// public endpoint pins Status verbatim and defaults SkipTotal to
// true; the user-facing template can opt in to the count via
// ?total=1 if it wants to render a "X results" line.
func parsePublicOpts(r *http.Request) pkgsearch.SearchOpts {
	q := r.URL.Query()
	opts := pkgsearch.SearchOpts{
		Status:    publicStatus,
		SkipTotal: true,
	}
	if t := q.Get("types"); t != "" {
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
	if raw := q.Get("total"); raw != "" {
		// Truthy values opt back into the COUNT query. We match the
		// strconv.ParseBool surface so "1", "t", "true" all work.
		if v, err := strconv.ParseBool(raw); err == nil {
			opts.SkipTotal = !v
		}
	}
	return opts
}

// Mount registers the handler at base + "/search" with an IP-keyed
// rate limit wrapper. The limiter is the caller's responsibility to
// configure (typically a 5-req-per-second token bucket); we just
// install the middleware around the handler.
//
// base is the public REST root, typically "/api/v1".
func Mount(mux *http.ServeMux, base string, limiter ratelimit.Limiter, h *Handler) error {
	if h == nil {
		return errors.New("rest/search.Mount: handler is required")
	}
	if limiter == nil {
		// We do allow a nil limiter for local/dev wiring; production
		// boot threads a Redis-backed limiter. Mount returns an
		// error so the boot path can surface the wiring choice
		// explicitly if the operator wants to enforce a limit.
		mux.Handle("GET "+base+"/search", h)
		return nil
	}
	mw := ratelimit.Middleware(limiter, ratelimit.KeyByIP)
	mux.Handle("GET "+base+"/search", mw(h))
	return nil
}
