package media

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
		return errors.New("rest/media: Store is required")
	}
	return nil
}

type handlers struct {
	store  Store
	logger *slog.Logger
}

// Mount wires the public media routes onto mux under base (typically
// "/api/v1/media").
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{store: deps.Store, logger: deps.Logger}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, http.HandlerFunc(h.list))
	mux.Handle("GET "+base+"/{id}", http.HandlerFunc(h.get))
	return nil
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
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

	mimeClass := strings.TrimSpace(q.Get("mime_class"))
	switch mimeClass {
	case "", "image", "video", "document":
		// ok
	default:
		router.WriteError(w, http.StatusBadRequest, "invalid_mime_class",
			"mime_class must be one of image|video|document")
		return
	}

	rows, err := h.store.List(r.Context(), ListFilter{
		MimeClass: mimeClass,
		Limit:     limit,
		After:     after,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/media: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to list media")
		return
	}

	var next string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = router.EncodeCursor(last.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00") + ":" + last.ID)
	}

	router.WriteJSON(w, http.StatusOK, router.Page[Asset]{
		Data: rows,
		Pagination: router.PageInfo{
			NextCursor: next,
		},
	})
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id is required")
		return
	}
	a, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "media not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/media: get failed",
			slog.String("id", id),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to fetch media")
		return
	}
	router.WriteJSON(w, http.StatusOK, a)
}
