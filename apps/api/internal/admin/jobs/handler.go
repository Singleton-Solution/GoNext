package jobs

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/hibiken/asynq"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// payloadPreviewLen is the maximum number of bytes of the payload
// surfaced in the list view's payload_preview field. 200 chars is the
// sweet spot that lets the table render a useful peek without bloating
// each row to multiple lines of JSON. The detail endpoint returns the
// full payload, so this is purely cosmetic.
const payloadPreviewLen = 200

// defaultListLimit is the page size when the client supplies no limit.
// Matches the rest of the admin REST surface (posts, pages) for muscle
// memory.
const defaultListLimit = 30

// maxListLimit caps the page size. Asynq itself paginates at the same
// boundary; going higher is a Redis round-trip waste.
const maxListLimit = 100

// redactedSentinel is what we substitute into the payload preview when
// a field has been redacted. Operators recognise this string from the
// audit log; using a literal "***REDACTED***" is intentional.
const redactedSentinel = "***REDACTED***"

// Deps is the dependency bag for Mount. Every field is required;
// validate() catches missing fields at boot rather than NPE'ing on the
// first request.
type Deps struct {
	// Inspector is the Asynq inspector (or a test fake satisfying the
	// interface). Required.
	Inspector Inspector

	// Redactions persists per-task redaction records. Required.
	Redactions RedactionStore

	// Policy resolves the jobs.admin capability check. Required.
	Policy policy.Policy

	// Logger receives structured log lines. nil falls back to
	// slog.Default — useful for tests but production wiring should
	// always pass a service logger.
	Logger *slog.Logger

	// CurrentUserID, when non-nil, is called per-request to resolve the
	// operator's user ID for the redaction record's redacted_by column.
	// Production wires this to the auth-session middleware's principal
	// reader; tests can supply a static stub. nil falls back to the
	// principal's UserID.
	CurrentUserID func(*http.Request) string
}

func (d Deps) validate() error {
	if d.Inspector == nil {
		return errors.New("admin/jobs: Inspector is required")
	}
	if d.Redactions == nil {
		return errors.New("admin/jobs: Redactions store is required")
	}
	if d.Policy == nil {
		return errors.New("admin/jobs: Policy is required")
	}
	return nil
}

// handlers is the resolved-Deps form passed around inside the package.
// Built once by Mount and shared across the registered routes.
type handlers struct {
	inspector  Inspector
	redactions RedactionStore
	policy     policy.Policy
	logger     *slog.Logger
	currentUID func(*http.Request) string
}

// Mount wires the DLQ admin routes onto mux under base (typically
// "/api/v1/admin/jobs"). Returns an error rather than panicking if Deps
// is malformed so the boot path can surface it cleanly.
//
// The route tree:
//
//	GET    {base}/dlq                 — list archived tasks
//	GET    {base}/dlq/{id}            — fetch a single archived task
//	POST   {base}/dlq/{id}/replay     — replay (archived → pending)
//	POST   {base}/dlq/{id}/discard    — delete the task
//	POST   {base}/dlq/{id}/redact     — apply a redaction mask
//
// Every route is gated by the jobs.admin capability. The list endpoint
// includes the gate too because payload previews can leak data — a
// drive-by curl shouldn't get a peek at customer state.
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

	h := &handlers{
		inspector:  deps.Inspector,
		redactions: deps.Redactions,
		policy:     deps.Policy,
		logger:     deps.Logger,
		currentUID: deps.CurrentUserID,
	}

	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base+"/dlq", h.gate(h.list))
	mux.Handle("GET "+base+"/dlq/{id}", h.gate(h.get))
	mux.Handle("POST "+base+"/dlq/{id}/replay", h.gate(h.replay))
	mux.Handle("POST "+base+"/dlq/{id}/discard", h.gate(h.discard))
	mux.Handle("POST "+base+"/dlq/{id}/redact", h.gate(h.redact))
	return nil
}

// gate wraps a handler with the auth + jobs.admin capability check.
// Returns 401 if no principal is on the context (the auth middleware
// hasn't run, or the request is anonymous); 403 if the principal lacks
// the capability.
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if d := h.policy.Can(pr, policy.CapJobsAdmin, nil); !d.Allowed {
			router.WriteError(w, http.StatusForbidden, "forbidden", d.Reason)
			return
		}
		next(w, r, pr)
	})
}

// list handles GET /dlq. Query params:
//
//	queue   — required; the Asynq queue to inspect (e.g. "default").
//	limit   — optional; page size, 1..100, default 30.
//	cursor  — optional; opaque cursor encoded by router.EncodeCursor.
//	          The decoded value is the 1-based page number — Asynq's
//	          Inspector paginates by page number, not by ID.
//
// We keep the cursor opaque so the UI doesn't grow a dependency on the
// "page number" detail. If Asynq's pagination model ever changes (e.g.
// to ID-based), we change the encoding here without touching the UI.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	queue := r.URL.Query().Get("queue")
	if queue == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_queue", "queue query parameter is required")
		return
	}

	limit := defaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			router.WriteError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		if n > maxListLimit {
			n = maxListLimit
		}
		limit = n
	}

	page := 1
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := router.ParseCursor(raw)
		if err != nil {
			router.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is malformed")
			return
		}
		// The cursor encodes the next page number as decimal text. We
		// store text, not binary, because the encoded form is short and
		// debuggable in browser dev tools.
		n, err := strconv.Atoi(decoded)
		if err != nil || n < 1 {
			router.WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is malformed")
			return
		}
		page = n
	}

	// Pull limit+1 so we know whether to surface a next_cursor without
	// a second round-trip. Asynq doesn't tell us "is there more?" in
	// its list response; the overshoot is the standard trick.
	tasks, err := h.inspector.ListArchivedTasks(queue, asynq.PageSize(limit+1), asynq.Page(page))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/jobs: list failed",
			slog.String("queue", queue),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list archived tasks")
		return
	}

	// Compute redaction records in one round-trip. The redaction layer
	// is the only Postgres call on this hot path.
	idsForLookup := make([]string, 0, len(tasks))
	pageTasks := tasks
	hasNext := false
	if len(tasks) > limit {
		hasNext = true
		pageTasks = tasks[:limit]
	}
	for _, t := range pageTasks {
		idsForLookup = append(idsForLookup, t.ID)
	}
	reds, err := h.redactions.GetMany(r.Context(), queue, idsForLookup)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin/jobs: redaction lookup failed",
			slog.String("queue", queue),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to load redactions")
		return
	}

	out := make([]ArchivedTask, 0, len(pageTasks))
	for _, t := range pageTasks {
		out = append(out, toArchivedTask(t, reds[t.ID], false))
	}

	var nextCursor string
	if hasNext {
		nextCursor = router.EncodeCursor(strconv.Itoa(page + 1))
	}

	page1 := router.Page[ArchivedTask]{
		Data: out,
		Pagination: router.PageInfo{
			NextCursor: nextCursor,
		},
	}
	router.WriteJSON(w, http.StatusOK, page1)
}

// get handles GET /dlq/{id}. The query param `queue` is still required
// — Asynq's GetTaskInfo is keyed by (queue, id), and there is no global
// "find this task" API. The UI knows the queue from the row it was
// rendered in.
func (h *handlers) get(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "task id is required")
		return
	}
	queue := r.URL.Query().Get("queue")
	if queue == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_queue", "queue query parameter is required")
		return
	}

	task, err := h.inspector.GetTaskInfo(queue, id)
	if err != nil {
		h.writeInspectorError(w, r, err, "admin/jobs.get")
		return
	}

	red, _ := h.redactions.Get(r.Context(), queue, id)
	router.WriteJSON(w, http.StatusOK, toArchivedTask(task, red, true))
}

// writeInspectorError translates Asynq's sentinel errors to HTTP. Any
// other error is treated as internal and the original is logged.
func (h *handlers) writeInspectorError(w http.ResponseWriter, r *http.Request, err error, tag string) {
	switch {
	case errors.Is(err, asynq.ErrTaskNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", "task not found")
	case errors.Is(err, asynq.ErrQueueNotFound):
		router.WriteError(w, http.StatusNotFound, "queue_not_found", "queue not found")
	default:
		h.logger.ErrorContext(r.Context(), tag+": inspector error", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// toArchivedTask converts the upstream *asynq.TaskInfo + the redaction
// record to our on-wire shape. detail=true includes the full Payload;
// detail=false trims it to the 200-char preview only.
//
// Redaction semantics:
//
//   - When a redaction record exists, we attempt to parse the payload
//     as a JSON object and replace each redacted top-level field with
//     ***REDACTED***. Non-object payloads (raw bytes, JSON arrays) get
//     a wholesale ***REDACTED*** substitution because we can't
//     surgically mask a non-object.
//   - The redaction is applied to BOTH the preview and (when present)
//     the full payload — the detail endpoint must respect the mask too
//     or operators see different data depending on which page they're
//     on, which defeats the purpose.
func toArchivedTask(t *asynq.TaskInfo, red Redaction, detail bool) ArchivedTask {
	out := ArchivedTask{
		ID:        t.ID,
		Queue:     t.Queue,
		Type:      t.Type,
		LastError: t.LastErr,
		FailedAt:  t.LastFailedAt,
		Retried:   t.Retried,
		MaxRetry:  t.MaxRetry,
	}

	masked := applyRedaction(t.Payload, red)
	out.PayloadPreview = previewBytes(masked, payloadPreviewLen)
	if detail {
		out.Payload = masked
	}

	if len(red.Fields) > 0 {
		out.Redacted = true
		// Sort? We deliberately leave the order as recorded — operators
		// expect "the order I added them in" rather than alphabetical.
		out.RedactedFields = append([]string{}, red.Fields...)
	}
	return out
}

// previewBytes returns the first n bytes of b as a string, appending an
// ellipsis when the input was truncated. Used for the payload_preview
// field; safe on non-UTF8 inputs (it truncates at the byte boundary
// which is fine for a UI hint).
func previewBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
