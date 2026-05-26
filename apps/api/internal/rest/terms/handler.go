package terms

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
)

// Deps is the dependency bag for Mount.
type Deps struct {
	Store  Store
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("rest/terms: Store is required")
	}
	return nil
}

type handlers struct {
	store  Store
	logger *slog.Logger
}

// Mount wires the public terms + taxonomies routes under termsBase
// and taxonomiesBase (typically "/api/v1/terms" and
// "/api/v1/taxonomies"). They are separate paths because they're
// separate kinds of resource — terms belong to a taxonomy, not vice
// versa — but they share a Store and a Mount call for symmetry.
func Mount(mux *http.ServeMux, termsBase, taxonomiesBase string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{store: deps.Store, logger: deps.Logger}
	termsBase = strings.TrimRight(termsBase, "/")
	taxonomiesBase = strings.TrimRight(taxonomiesBase, "/")
	mux.Handle("GET "+termsBase, http.HandlerFunc(h.listTerms))
	mux.Handle("GET "+termsBase+"/{id}", http.HandlerFunc(h.getTerm))
	mux.Handle("GET "+taxonomiesBase, http.HandlerFunc(h.listTaxonomies))
	mux.Handle("GET "+taxonomiesBase+"/{slug}", http.HandlerFunc(h.getTaxonomy))
	return nil
}

func (h *handlers) listTaxonomies(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.ListTaxonomies(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/terms: list taxonomies failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to list taxonomies")
		return
	}
	router.WriteJSON(w, http.StatusOK, router.Page[Taxonomy]{Data: rows})
}

func (h *handlers) getTaxonomy(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_slug", "slug is required")
		return
	}
	t, err := h.store.GetTaxonomy(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "taxonomy not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/terms: get taxonomy failed",
			slog.String("slug", slug),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to fetch taxonomy")
		return
	}
	router.WriteJSON(w, http.StatusOK, t)
}

func (h *handlers) listTerms(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := DefaultListLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			router.WriteError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be a positive integer")
			return
		}
		if n > MaxListLimit {
			n = MaxListLimit
		}
		limit = n
	}

	var after string
	if raw := q.Get("after"); raw != "" {
		decoded, err := router.ParseCursor(raw)
		if err != nil {
			router.WriteError(w, http.StatusBadRequest, "invalid_cursor",
				"after must be a valid cursor")
			return
		}
		after = decoded
	}

	f := TermListFilter{
		Taxonomy: strings.TrimSpace(q.Get("taxonomy")),
		Search:   strings.TrimSpace(q.Get("search")),
		Limit:    limit,
		After:    after,
	}
	if _, ok := q["parent_id"]; ok {
		f.ParentPresent = true
		f.ParentID = strings.TrimSpace(q.Get("parent_id"))
	}

	rows, err := h.store.ListTerms(r.Context(), f)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/terms: list terms failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to list terms")
		return
	}

	var next string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = router.EncodeCursor(last.Path + ":" + last.ID)
	}

	router.WriteJSON(w, http.StatusOK, router.Page[Term]{
		Data: rows,
		Pagination: router.PageInfo{
			NextCursor: next,
		},
	})
}

func (h *handlers) getTerm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}
	t, err := h.store.GetTerm(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "term not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/terms: get term failed",
			slog.String("id", id),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to fetch term")
		return
	}
	router.WriteJSON(w, http.StatusOK, t)
}
