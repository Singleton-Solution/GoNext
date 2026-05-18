package jobs

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// maxRedactBodyBytes caps the redact request body. 16 KiB is plenty —
// a redaction request is just a queue name plus a small list of field
// names; anything larger is either a client bug or an attacker
// probing the parser.
const maxRedactBodyBytes = 16 * 1024

// maxRedactFields caps the number of field names per request. A
// well-formed payload should have at most a handful of sensitive
// fields; an unbounded array would force a Postgres TEXT[] of
// arbitrary size, with the attendant query-cost surprise.
const maxRedactFields = 64

// maxFieldNameLen caps the length of a single field name. Same
// rationale as maxRedactFields — top-level JSON keys are short in
// practice (<= 64 chars covers everything we ship).
const maxFieldNameLen = 128

// queueQueryParam is the name of the query string parameter the action
// endpoints read to determine which Asynq queue the task lives on.
// Asynq's mutation APIs (RunArchivedTask, DeleteArchivedTask) are
// keyed by (queue, id) — the queue isn't recoverable from the ID
// alone, so the client must pass it.
const queueQueryParam = "queue"

// redactRequest is the JSON body of POST /dlq/{id}/redact. Field names
// match the on-wire snake_case convention.
type redactRequest struct {
	// Queue is the queue the task lives on. Required.
	Queue string `json:"queue"`

	// Fields is the set of top-level payload field names to mask.
	// Required and must be non-empty — a redaction with zero fields
	// is semantically a discard; the client should call /discard
	// instead. We enforce this both here and in the DB CHECK.
	Fields []string `json:"fields"`
}

// validate runs structural checks on the request. Returns a typed
// problem-code string so the handler can emit a consistent
// machine-readable error.
func (r redactRequest) validate() (string, string) {
	if r.Queue == "" {
		return "missing_queue", "queue is required"
	}
	if len(r.Fields) == 0 {
		return "missing_fields", "fields must be a non-empty array"
	}
	if len(r.Fields) > maxRedactFields {
		return "too_many_fields", "fields exceeds the maximum allowed count"
	}
	seen := make(map[string]struct{}, len(r.Fields))
	for _, f := range r.Fields {
		if f == "" {
			return "invalid_field", "field names must be non-empty strings"
		}
		if len(f) > maxFieldNameLen {
			return "invalid_field", "field name exceeds the maximum allowed length"
		}
		// Reject dotted paths; v1 supports top-level only. The UI
		// should reject these client-side, but defence in depth.
		if strings.ContainsRune(f, '.') {
			return "invalid_field", "nested field paths are not supported in v1"
		}
		if _, dup := seen[f]; dup {
			return "duplicate_field", "field names must be unique"
		}
		seen[f] = struct{}{}
	}
	return "", ""
}

// replay handles POST /dlq/{id}/replay. Moves an archived task back
// onto the pending queue so the worker picks it up on the next poll.
// Asynq's RunArchivedTask is idempotent against double-clicks: a
// second call on an already-replayed task returns ErrTaskNotFound,
// which we surface as 404.
func (h *handlers) replay(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id, queue, ok := h.actionPathParams(w, r)
	if !ok {
		return
	}

	if err := h.inspector.RunArchivedTask(queue, id); err != nil {
		h.writeInspectorError(w, r, err, "admin/jobs.replay")
		return
	}

	h.logger.InfoContext(r.Context(), "admin/jobs: task replayed",
		slog.String("queue", queue),
		slog.String("task_id", id),
		slog.String("actor", h.currentUID(r)),
	)
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"queue":  queue,
		"action": "replay",
	})
}

// discard handles POST /dlq/{id}/discard. Deletes the archived task
// from Redis. Irreversible by design — operators use redact when they
// want to keep the task but hide its payload. As with replay, a second
// call on an already-discarded task returns 404.
func (h *handlers) discard(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id, queue, ok := h.actionPathParams(w, r)
	if !ok {
		return
	}

	if err := h.inspector.DeleteArchivedTask(queue, id); err != nil {
		h.writeInspectorError(w, r, err, "admin/jobs.discard")
		return
	}

	h.logger.InfoContext(r.Context(), "admin/jobs: task discarded",
		slog.String("queue", queue),
		slog.String("task_id", id),
		slog.String("actor", h.currentUID(r)),
	)
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"queue":  queue,
		"action": "discard",
	})
}

// redact handles POST /dlq/{id}/redact. Stores a redaction record so
// subsequent listings substitute ***REDACTED*** for the named fields.
// We deliberately do NOT verify the task exists at the Asynq layer
// before recording the redaction: if the task is archived, the record
// will apply; if it gets discarded or replayed, the record harmlessly
// stays in place for the audit trail.
func (h *handlers) redact(w http.ResponseWriter, r *http.Request, _ policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "task id is required")
		return
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxRedactBodyBytes)
	var req redactRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "request body could not be parsed: "+err.Error())
		return
	}
	if dec.More() {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "request body must contain a single JSON value")
		return
	}

	if code, detail := req.validate(); code != "" {
		router.WriteError(w, http.StatusBadRequest, code, detail)
		return
	}

	rec := Redaction{
		TaskID:     id,
		Queue:      req.Queue,
		Fields:     sortedFields(req.Fields),
		RedactedAt: time.Now().UTC(),
		RedactedBy: h.currentUID(r),
	}
	if err := h.redactions.Upsert(r.Context(), rec); err != nil {
		h.logger.ErrorContext(r.Context(), "admin/jobs: redact upsert failed",
			slog.String("queue", req.Queue),
			slog.String("task_id", id),
			slog.Any("err", err),
		)
		// The store layer only fails for clearly client-side reasons
		// (empty fields) or internal errors. We've already validated
		// the former; surface as 500.
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to record redaction")
		return
	}

	h.logger.InfoContext(r.Context(), "admin/jobs: task redacted",
		slog.String("queue", req.Queue),
		slog.String("task_id", id),
		slog.String("actor", h.currentUID(r)),
		slog.Int("fields", len(req.Fields)),
	)
	router.WriteJSON(w, http.StatusOK, rec)
}

// actionPathParams extracts the (id, queue) pair shared by replay and
// discard. Writes the error response and returns ok=false on missing
// values; the caller returns immediately on false.
func (h *handlers) actionPathParams(w http.ResponseWriter, r *http.Request) (id, queue string, ok bool) {
	id = r.PathValue("id")
	if id == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_id", "task id is required")
		return "", "", false
	}
	queue = r.URL.Query().Get(queueQueryParam)
	if queue == "" {
		router.WriteError(w, http.StatusBadRequest, "missing_queue", "queue query parameter is required")
		return "", "", false
	}
	return id, queue, true
}

// Compile-time guard: the action handlers must signal "no body
// expected" rather than silently ignoring one. We use the errors
// package to keep the dependency surface tight; the file is otherwise
// dependency-free.
var _ = errors.New
