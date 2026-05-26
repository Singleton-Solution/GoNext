// Package data — see doc.go for the package overview.
package data

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Event types emitted to the audit log. Exported so tests and SIEM
// dashboards can refer to the same canonical strings.
const (
	EventDataExportRequested = "account.data.export.requested"
	EventDataDeleteSucceeded = "account.data.delete.succeeded"
	EventDataDeleteFailed    = "account.data.delete.failed"
)

// purgeGrace is the recovery window between anonymisation and the
// final hard-delete. The cron task in apps/worker/internal/tasks/gdpr
// reads this value indirectly via the `users.scheduled_purge_at`
// column we stamp during the delete handler.
//
// Promoted to a const (not a config option) because changing it
// retroactively would change the meaning of every row already
// scheduled for purge — operators who want a longer window can backfill
// scheduled_purge_at directly.
const purgeGrace = 30 * 24 * time.Hour

// PasswordVerifier is the contract the delete handler needs to confirm
// the caller actually knows the current password. We accept an
// interface so tests inject a deterministic fake and the production
// code wires packages/go/auth/password.Verify behind it.
type PasswordVerifier interface {
	// Verify returns (ok, needsRehash, err) for the given plaintext
	// against the stored argon2id PHC string. Caller ignores
	// needsRehash here — rehashing on delete is pointless.
	Verify(ctx context.Context, userID, plaintext string) (ok bool, err error)
}

// Anonymizer is the contract for the delete path. The implementation
// runs a single transaction that:
//   1. zeroes PII columns on `users`
//   2. updates posts.author_id rows to NULL or the anonymous-user id
//   3. updates comments.author_id similarly
//   4. zeroes audit_log.user_agent and audit_log.ip where actor=user
//   5. stamps users.anonymized_at = now()
//   6. stamps users.scheduled_purge_at = now() + 30d
//
// We keep this behind an interface so the handler stays small and the
// SQL lives in one place (a follow-up PR adds the pgx implementation —
// the handler can ship with a memory implementation for tests).
type Anonymizer interface {
	Anonymize(ctx context.Context, userID string) error
}

// ExportEnqueuer hands an export request off to the worker. Returns a
// stable job id we surface to the caller for polling. Implementations
// are expected to be cheap (a single Redis LPUSH); the heavy lifting
// happens in apps/worker.
type ExportEnqueuer interface {
	Enqueue(ctx context.Context, userID, jobID string) error
}

// AuditEmitter mirrors sessions.AuditEmitter — narrow interface so
// callers who wrap audit.Emitter for tracing keep working.
type AuditEmitter interface {
	Emit(ctx context.Context, eventType string, opts ...audit.EmitOption) error
}

// Deps is the constructor input for Handlers. All fields are required;
// passing a zero value panics at NewHandlers time (a wiring bug should
// crash at boot, not surface as a 500).
type Deps struct {
	Verifier   PasswordVerifier
	Anonymizer Anonymizer
	Enqueuer   ExportEnqueuer
	Audit      AuditEmitter
	Log        *slog.Logger
	// PollURLBase is the public origin for the export-job polling URL
	// surfaced in the API response (e.g. "https://api.example.com").
	// The handler appends "/api/v1/account/data/export/{jobID}".
	PollURLBase string
}

// Handlers carries per-process deps. Safe for concurrent use.
type Handlers struct {
	verifier   PasswordVerifier
	anon       Anonymizer
	enq        ExportEnqueuer
	audit      AuditEmitter
	log        *slog.Logger
	pollBase   string
}

// NewHandlers panics on missing required deps. Logger defaults to
// slog.Default. PollURLBase falls back to a relative URL when empty
// (admin UIs running on the same origin).
func NewHandlers(d Deps) *Handlers {
	if d.Verifier == nil {
		panic("data.NewHandlers: Verifier is required")
	}
	if d.Anonymizer == nil {
		panic("data.NewHandlers: Anonymizer is required")
	}
	if d.Enqueuer == nil {
		panic("data.NewHandlers: Enqueuer is required")
	}
	if d.Audit == nil {
		panic("data.NewHandlers: Audit is required")
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handlers{
		verifier: d.Verifier,
		anon:     d.Anonymizer,
		enq:      d.Enqueuer,
		audit:    d.Audit,
		log:      log,
		pollBase: strings.TrimRight(d.PollURLBase, "/"),
	}
}

// Routes returns a sub-mux at "/api/v1/account/data". The caller mounts
// it under RequireSession middleware; the export route additionally
// belongs behind a 1/day rate limiter applied at mount time (we don't
// hard-code the limiter here so tests can run without Redis).
func (h *Handlers) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /export", h.Export)
	mux.HandleFunc("POST /delete", h.Delete)
	return mux
}

// ExportResponse is the JSON shape returned by GET /api/v1/account/data/export.
type ExportResponse struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	PollURL   string    `json:"poll_url"`
	CreatedAt time.Time `json:"created_at"`
}

// Export enqueues an export job and returns its id + a polling URL.
// The actual ZIP is assembled by the worker (see
// apps/worker/internal/tasks/gdpr). Status begins as "queued"; the
// poll endpoint (built in a follow-up issue) flips it to "ready" once
// the worker has uploaded the artifact.
func (h *Handlers) Export(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	jobID := newJobID()
	if err := h.enq.Enqueue(r.Context(), p.UserID, jobID); err != nil {
		h.log.WarnContext(r.Context(), "data.export: enqueue failed",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeError(w, http.StatusServiceUnavailable, "enqueue_failed")
		return
	}

	// Audit emit failures are non-fatal — the export was already
	// scheduled and the user is owed a 202. We log a warning so the
	// operator notices the audit pipeline is down.
	if err := h.audit.Emit(r.Context(), EventDataExportRequested,
		audit.WithTarget("user", p.UserID),
		audit.WithMetadata(map[string]any{"job_id": jobID}),
		audit.WithSeverity(audit.SeverityInfo),
	); err != nil {
		h.log.WarnContext(r.Context(), "data.export: audit emit failed",
			slog.String("err", err.Error()))
	}

	pollURL := fmt.Sprintf("%s/api/v1/account/data/export/%s", h.pollBase, jobID)

	writeJSON(w, http.StatusAccepted, ExportResponse{
		JobID:     jobID,
		Status:    "queued",
		PollURL:   pollURL,
		CreatedAt: time.Now().UTC(),
	})
}

// deleteRequest is the POST body for the delete handler. Both fields
// are required; the handler checks them on every request even though
// the second is a duplicate of the first — operators have repeatedly
// asked for the "type your password twice" UX on irreversible flows.
type deleteRequest struct {
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

// DeleteResponse is the success body for POST /api/v1/account/data/delete.
// The user's session is also invalidated by the mount-time middleware
// (the caller wires DeleteAllForUser on the session manager); the body
// here surfaces the purge timeline so the admin UI can render a "your
// data will be permanently removed on YYYY-MM-DD" line.
type DeleteResponse struct {
	AnonymizedAt     time.Time `json:"anonymized_at"`
	ScheduledPurgeAt time.Time `json:"scheduled_purge_at"`
}

// Delete anonymises the user and stamps the 30-day purge deadline.
// The session middleware is responsible for clearing the request's
// session cookie on the way out — we focus on the irreversible
// database mutation here.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "missing_body")
		return
	}
	defer r.Body.Close()

	var req deleteRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.Password == "" || req.PasswordConfirm == "" {
		writeError(w, http.StatusBadRequest, "password_required")
		return
	}
	// Constant-time-ish check is fine here — the strings are not
	// secrets relative to each other; the secret is whether either
	// matches the stored hash.
	if req.Password != req.PasswordConfirm {
		writeError(w, http.StatusBadRequest, "password_mismatch")
		return
	}

	ok, err := h.verifier.Verify(r.Context(), p.UserID, req.Password)
	if err != nil {
		h.log.ErrorContext(r.Context(), "data.delete: verify error",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if !ok {
		// Audit the failed attempt with severity warn so the security
		// team's "delete attempts by IP" dashboard picks it up.
		_ = h.audit.Emit(r.Context(), EventDataDeleteFailed,
			audit.WithTarget("user", p.UserID),
			audit.WithSeverity(audit.SeverityWarning),
		)
		writeError(w, http.StatusUnauthorized, "invalid_password")
		return
	}

	if err := h.anon.Anonymize(r.Context(), p.UserID); err != nil {
		h.log.ErrorContext(r.Context(), "data.delete: anonymize failed",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	now := time.Now().UTC()
	purgeAt := now.Add(purgeGrace)

	if err := h.audit.Emit(r.Context(), EventDataDeleteSucceeded,
		audit.WithTarget("user", p.UserID),
		audit.WithMetadata(map[string]any{
			"anonymized_at":      now.Format(time.RFC3339),
			"scheduled_purge_at": purgeAt.Format(time.RFC3339),
		}),
		audit.WithSeverity(audit.SeverityInfo),
	); err != nil {
		h.log.WarnContext(r.Context(), "data.delete: audit emit failed",
			slog.String("err", err.Error()))
	}

	writeJSON(w, http.StatusOK, DeleteResponse{
		AnonymizedAt:     now,
		ScheduledPurgeAt: purgeAt,
	})
}

// --- helpers ----------------------------------------------------------

// newJobID returns a 16-byte hex string. Crypto/rand is fine here
// (the surface is rate-limited and the id is opaque to the caller).
func newJobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Crypto/rand failures are catastrophic — every TLS handshake
		// also depends on this entropy source. Panic surfaces the issue
		// loudly rather than silently degrading to a deterministic id.
		panic(fmt.Sprintf("data.newJobID: rand.Read: %v", err))
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Best-effort log; the response is already committed.
		slog.Default().Warn("data: encode response failed", slog.String("err", err.Error()))
	}
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code": code,
		},
	})
}

func principal(ctx context.Context) (policy.Principal, bool) {
	p, ok := policy.FromContext(ctx)
	if !ok || p.UserID == "" {
		return policy.Principal{}, false
	}
	return p, true
}

// Sentinel errors that anonymiser implementations may return. Exposed
// so tests and the handler can assert against them without string
// comparison.
var (
	ErrAlreadyAnonymized = errors.New("user already anonymised")
	ErrUserNotFound      = errors.New("user not found")
)
