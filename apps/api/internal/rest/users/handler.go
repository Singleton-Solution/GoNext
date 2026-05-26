package users

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
)

// Deps is the dependency bag for Mount. Store is required; the rest
// have defaults.
type Deps struct {
	Store  Store
	Logger *slog.Logger
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("rest/users: Store is required")
	}
	return nil
}

// handlers is the resolved-Deps form.
type handlers struct {
	store  Store
	logger *slog.Logger
}

// Mount wires the public users routes onto mux under base (typically
// "/api/v1/users"). Two routes:
//
//	GET {base}            — list public users
//	GET {base}/{id}       — fetch by UUID OR by handle
//
// The "id-or-handle" dispatch happens inside the handler — see get().
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	h := &handlers{
		store:  deps.Store,
		logger: deps.Logger,
	}
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

	rows, err := h.store.List(r.Context(), ListFilter{
		HandlePrefix: strings.TrimSpace(q.Get("handle_prefix")),
		Limit:        limit,
		After:        after,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rest/users: list failed", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to list users")
		return
	}

	// Limit+1 → next-cursor trick: the store returned at most limit+1
	// rows; if it returned exactly limit+1, there's a next page and
	// the cursor is the last-but-one row.
	var next string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = router.EncodeCursor(last.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00") + ":" + last.ID)
	}

	router.WriteJSON(w, http.StatusOK, router.Page[User]{
		Data: rows,
		Pagination: router.PageInfo{
			NextCursor: next,
		},
	})
}

// get handles GET {base}/{id}. The {id} segment is treated as a UUID
// when it parses as one (36 chars + dashes) and as a handle otherwise.
// The dispatch happens here rather than as two separate routes because
// the path-pattern matcher in net/http doesn't let us discriminate on
// segment shape.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	idOrHandle := r.PathValue("id")
	if idOrHandle == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "id or handle is required")
		return
	}

	var (
		u   User
		err error
	)
	if looksLikeUUID(idOrHandle) {
		u, err = h.store.GetByID(r.Context(), idOrHandle)
	} else {
		u, err = h.store.GetByHandle(r.Context(), idOrHandle)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			router.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "rest/users: get failed",
			slog.String("id_or_handle", idOrHandle),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error",
			"failed to fetch user")
		return
	}

	router.WriteJSON(w, http.StatusOK, u)
}

// looksLikeUUID is a cheap shape check — we accept a 36-character
// string with dashes at the canonical positions (8-4-4-4-12). We do
// NOT call uuid.Parse here because (a) it would force the package to
// take a uuid dependency for a shape probe, and (b) a non-UUID-shaped
// handle is fine to dispatch directly to GetByHandle.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			// hex digit
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
