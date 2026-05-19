package comments

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// defaultListLimit is the page size when the client supplies no
// limit. Matches the rest of the admin REST surface.
const defaultListLimit = 30

// maxListLimit caps the page size. Higher values are silently
// clamped — operators don't need to see more than 100 comments at a
// time in one page and the back-end query becomes increasingly
// expensive past that.
const maxListLimit = 100

// maxBulkIDs caps the number of IDs a single bulk request can carry.
// Picked so a moderator cannot accidentally fire a single
// transaction that walks the entire pending queue. Operators who
// need bigger batches can issue successive requests.
const maxBulkIDs = 500

// Deps is the dependency bag for Mount. Every required field is
// non-nil; Logger falls back to slog.Default for convenience.
type Deps struct {
	// Store persists comments. Required.
	Store Store

	// Policy gates the moderate_comments capability check.
	// Required.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production should pass
	// a service logger.
	Logger *slog.Logger

	// CurrentUserID, when non-nil, is called per-request to resolve
	// the operator's user ID for the reply handler's author link.
	// nil falls back to the principal's UserID.
	CurrentUserID func(*http.Request) string

	// CurrentDisplayName, when non-nil, is called per-request to
	// resolve the operator's display name. Production wires this
	// to the user-profile reader; tests pass a static stub.
	// nil yields an empty string, in which case the store falls
	// back to "Moderator".
	CurrentDisplayName func(*http.Request) string
}

func (d Deps) validate() error {
	if d.Store == nil {
		return errors.New("admin/comments: Store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/comments: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the
// package. Built once by Mount and shared across the routes.
type handlers struct {
	store          Store
	policy         policy.Policy
	logger         *slog.Logger
	currentUID     func(*http.Request) string
	currentDisplay func(*http.Request) string
}

// Mount wires the comments admin routes onto mux under base
// (typically "/api/v1/admin/comments"). Returns an error rather
// than panicking if Deps is malformed so the boot path can surface
// it cleanly.
//
// Route tree:
//
//	GET    {base}                — list, paginated
//	PATCH  {base}/{id}           — single-row status transition
//	POST   {base}/bulk           — atomic bulk action
//	POST   {base}/{id}/reply     — moderator reply
//
// Every route is gated by the moderate_comments capability. The
// list endpoint is gated too because comment bodies can carry PII
// (mod queue contains anonymous submissions with whatever the
// commenter typed).
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.CurrentUserID == nil {
		deps.CurrentUserID = func(r *http.Request) string {
			if pr, ok := policy.FromContext(r.Context()); ok {
				return pr.UserID
			}
			return ""
		}
	}
	if deps.CurrentDisplayName == nil {
		deps.CurrentDisplayName = func(*http.Request) string { return "" }
	}

	h := &handlers{
		store:          deps.Store,
		policy:         deps.Policy,
		logger:         deps.Logger,
		currentUID:     deps.CurrentUserID,
		currentDisplay: deps.CurrentDisplayName,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, h.gate(h.list))
	mux.Handle("POST "+base+"/bulk", h.gate(h.bulk))
	mux.Handle("POST "+base+"/{id}/reply", h.gate(h.reply))
	mux.Handle("PATCH "+base+"/{id}", h.gate(h.update))
	return nil
}

// gate wraps a handler with the auth + moderate_comments capability
// check. Returns 401 if no principal is on the context, 403 if the
// principal lacks the capability.
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, policy.CapModerateComments, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// parseLimit parses ?limit= with the standard 1..maxListLimit clamp
// and default. Returns the validated limit or a non-nil error if
// the value is malformed (negative, non-integer, zero).
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultListLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("limit must be a positive integer")
	}
	if n > maxListLimit {
		n = maxListLimit
	}
	return n, nil
}

// parsePage parses ?page= with a default of 1.
func parsePage(raw string) (int, error) {
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("page must be a positive integer")
	}
	return n, nil
}
