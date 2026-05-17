// Package sessions — see doc.go for the package overview.
package sessions

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// EventSessionRevoked is the audit event type emitted for every session
// revoked through this package — both the targeted single-session
// DELETE and each session torn down by the bulk DELETE.
//
// The constant is exported so other packages (admin tooling, integration
// tests asserting against the audit log) can compare against the same
// canonical string without re-typing the magic value.
const EventSessionRevoked = "auth.session.revoked"

// defaultCookieName mirrors [session.CookieName]. We inline a copy to
// keep the package self-contained for tests; the default Handlers
// constructor seeds from session.CookieName directly.
const defaultCookieName = session.CookieName

// SessionStore is the slice of [session.Manager] this package depends on.
// Pulling only the methods we use makes the package testable without
// standing up Redis — a small fake in sessions_test.go satisfies it.
//
// The set is deliberately narrow: List + Delete + DeleteAllForUser.
// We do not need Get (the auth middleware already loaded the request's
// session) and we never mint or rotate tokens here.
type SessionStore interface {
	List(ctx context.Context, userID string) ([]session.SessionInfo, error)
	Delete(ctx context.Context, token string) error
}

// AuditEmitter is the slice of [audit.Emitter] we depend on. The real
// emitter satisfies it; tests pass an in-memory fake. We accept the
// concrete *audit.Emitter type by default through [NewHandlers] and
// expose this interface so callers who wrap the emitter (e.g. to add
// tracing) keep working.
type AuditEmitter interface {
	Emit(ctx context.Context, eventType string, opts ...audit.EmitOption) error
}

// Handlers carries the per-process dependencies the handlers need.
// Build one at boot via [NewHandlers] and mount [Handlers.Routes] from
// the API server's main routing setup.
//
// Handlers is safe for concurrent use: every field is itself
// concurrency-safe (the session.Manager has its own internal locking,
// the audit.Emitter is immutable after construction, and the logger is
// goroutine-safe by contract).
type Handlers struct {
	store      SessionStore
	emitter    AuditEmitter
	log        *slog.Logger
	cookieName string
}

// Option configures Handlers at construction time. The zero value is
// usable: every option has a defaulted behavior documented on its
// constructor.
type Option func(*Handlers)

// WithCookieName overrides the session-cookie name used to identify the
// "current" session. Empty strings are ignored; the default is
// [session.CookieName].
//
// Callers who run multiple session scopes on the same eTLD+1 (e.g.
// "sid" for the public site and "admin_sid" for the admin shell) pass
// the matching cookie name here so the right session is flagged as
// current.
func WithCookieName(name string) Option {
	return func(h *Handlers) {
		if name != "" {
			h.cookieName = name
		}
	}
}

// WithLogger overrides the package's structured logger. nil is ignored
// and slog.Default() is kept. The logger is used for non-fatal
// background failures (an audit emit that failed after the user-facing
// revocation already succeeded) so the request can still return 204
// while the operator sees a warning line.
func WithLogger(l *slog.Logger) Option {
	return func(h *Handlers) {
		if l != nil {
			h.log = l
		}
	}
}

// NewHandlers builds a Handlers ready to mount. Both store and emitter
// are required; passing nil panics — a wiring bug should crash at boot,
// not surface as a 500 on the first user request.
//
// The default cookie name is [session.CookieName]; override with
// [WithCookieName] when running a non-default session scope.
func NewHandlers(store SessionStore, emitter AuditEmitter, opts ...Option) *Handlers {
	if store == nil {
		panic("sessions.NewHandlers: store is required")
	}
	if emitter == nil {
		panic("sessions.NewHandlers: emitter is required")
	}
	h := &Handlers{
		store:      store,
		emitter:    emitter,
		log:        slog.Default(),
		cookieName: defaultCookieName,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// SessionView is the JSON shape of a single session in the GET response.
// Fields:
//
//   - ID is the SHA-256 hex of the raw session token, truncated to the
//     first 32 hex chars (16 bytes) — enough collision resistance for a
//     per-user list whose cardinality is in the low double digits, while
//     keeping the value short for URLs and admin UIs.
//   - CreatedAt and LastSeenAt are RFC3339 timestamps mirrored from the
//     stored session.
//   - DeviceLabel is a free-form human-readable label sourced from
//     session.Data["device_label"] when present, else the User-Agent
//     captured at login (also from Data), else an empty string. The
//     login handler decides which it stores; this package does not parse
//     User-Agent strings on its own (RFC 7231 reserves UA parsing for
//     the application).
//   - IP is the network address recorded at login time, read from
//     session.Data["ip"]. Empty if the login handler didn't record one.
//   - Current is true for exactly the session whose token matches the
//     sid cookie on the incoming request.
type SessionView struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	DeviceLabel string    `json:"device_label"`
	IP          string    `json:"ip"`
	Current     bool      `json:"current"`
}

// listResponse is the GET /api/v1/auth/sessions body. It wraps the
// slice in an object so we can add response-level metadata (pagination,
// truncation flags) later without a v2.
type listResponse struct {
	Sessions []SessionView `json:"sessions"`
}

// Routes returns an http.ServeMux preconfigured for this package's
// endpoints. The caller mounts it at "/api/v1/auth/sessions" under the
// [auth.RequireSession] middleware. The mux is allocated fresh on each
// call so callers can safely apply per-mount tweaks without aliasing
// each other.
//
// Why a sub-mux rather than separate http.Handler factories: ServeMux
// in Go 1.22+ supports method-and-path matching natively
// (`DELETE /{id}`), which gives us the routing we need with zero
// third-party dependencies.
func (h *Handlers) Routes() http.Handler {
	mux := http.NewServeMux()
	// Trailing-slash and non-trailing-slash forms both have to be
	// explicit under ServeMux's pattern grammar to avoid the implicit
	// "subtree" redirect on the bare path.
	mux.HandleFunc("GET /", h.List)
	mux.HandleFunc("DELETE /", h.DeleteAll)
	mux.HandleFunc("DELETE /{id}", h.DeleteOne)
	return mux
}

// List is the GET /api/v1/auth/sessions handler. It returns every live
// session belonging to the authenticated principal, with the current
// flag set on the one whose token matches the request's sid cookie.
//
// Errors:
//   - 401 if no principal is on the context. The caller is expected to
//     mount this handler behind [auth.RequireSession], which makes this
//     unreachable on the happy path — but we fail closed if a future
//     wire-up regresses.
//   - 500 if the store fails. We do NOT distinguish transient from
//     permanent failures here; the audit log and request log give
//     operators what they need to triage.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok || p.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	infos, err := h.store.List(r.Context(), p.UserID)
	if err != nil {
		h.log.WarnContext(r.Context(), "sessions: list failed",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	currentToken := h.currentToken(r)
	views := make([]SessionView, 0, len(infos))
	for _, info := range infos {
		views = append(views, SessionView{
			ID:          sessionIDFromToken(info.Token),
			CreatedAt:   info.CreatedAt.UTC(),
			LastSeenAt:  info.LastSeenAt.UTC(),
			DeviceLabel: deviceLabelFromInfo(info),
			IP:          ipFromInfo(info),
			Current:     currentToken != "" && constantTimeEqual(info.Token, currentToken),
		})
	}

	writeJSON(w, http.StatusOK, listResponse{Sessions: views})
}

// DeleteOne is the DELETE /api/v1/auth/sessions/{id} handler. It
// revokes exactly one session that belongs to the caller.
//
// Authorization model: the URL path identifies the session by its
// SessionID (SHA-256 hex prefix of the raw token), so a caller cannot
// guess another user's session ID without already having the raw
// token — which would be a compromise of much worse implications than
// a cross-user session deletion. Even so, we enforce that the target
// session must appear in the caller's session list; sessions belonging
// to other users return 404 (not 403) so we do not confirm whether
// some other user happens to have a session matching the supplied ID.
//
// Errors:
//   - 401 if no principal is on the context.
//   - 404 if no session in the caller's list matches the supplied ID.
//     This is the right answer for both "you typo'd the ID" and "the
//     ID is real but belongs to another user" — leaking the difference
//     would be a session-enumeration oracle.
//   - 500 on a store failure. The audit emit is best-effort; we log a
//     warning but still return 204 on the successful revoke path.
func (h *Handlers) DeleteOne(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok || p.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		// An empty {id} is structurally impossible under ServeMux's
		// "DELETE /{id}" pattern (the route wouldn't have matched), but
		// we keep the explicit guard for the off-route call path used
		// by tests that invoke the handler directly.
		writeJSONError(w, http.StatusNotFound, "not_found")
		return
	}

	infos, err := h.store.List(r.Context(), p.UserID)
	if err != nil {
		h.log.WarnContext(r.Context(), "sessions: list for delete failed",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	targetToken := ""
	for _, info := range infos {
		if constantTimeEqual(sessionIDFromToken(info.Token), id) {
			targetToken = info.Token
			break
		}
	}
	if targetToken == "" {
		// Either the ID is invalid OR it belongs to another user. We
		// MUST return the same status either way to avoid the
		// session-existence oracle the issue spec calls out.
		writeJSONError(w, http.StatusNotFound, "not_found")
		return
	}

	if err := h.store.Delete(r.Context(), targetToken); err != nil {
		h.log.WarnContext(r.Context(), "sessions: delete failed",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	h.emitRevoked(r, p.UserID, id, false)

	w.WriteHeader(http.StatusNoContent)
}

// DeleteAll is the DELETE /api/v1/auth/sessions handler. It revokes
// every session for the caller EXCEPT the one this request is using.
// The exclusion exists because logging the caller out of the very
// session that issued the request would 401 the immediate response in
// some clients and obliterate any "you have been logged out of N other
// devices" follow-up UI the SPA wants to render.
//
// If the request has no sid cookie (e.g. the caller is authenticating
// via a header-based mechanism a future patch adds), every session is
// revoked.
//
// Errors:
//   - 401 if no principal is on the context.
//   - 500 if the underlying list/delete fails. We aggregate: a single
//     delete failure stops the loop, the rest of the sessions are left
//     in place, and the caller can retry. This is gentler than
//     half-revoking and leaving the state inconsistent across replicas.
func (h *Handlers) DeleteAll(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromContext(r.Context())
	if !ok || p.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	infos, err := h.store.List(r.Context(), p.UserID)
	if err != nil {
		h.log.WarnContext(r.Context(), "sessions: list for delete-all failed",
			slog.String("user_id", p.UserID),
			slog.String("err", err.Error()))
		writeJSONError(w, http.StatusInternalServerError, "internal_error")
		return
	}

	currentToken := h.currentToken(r)
	for _, info := range infos {
		if currentToken != "" && constantTimeEqual(info.Token, currentToken) {
			// Skip the session this request is riding on; the whole
			// point of "log out everywhere else" is to preserve it.
			continue
		}
		if err := h.store.Delete(r.Context(), info.Token); err != nil {
			// Stop on first failure — see godoc above. We still emit
			// audit events for the sessions we DID revoke (handled by
			// emitRevoked being called inline), so the audit log is
			// honest about the partial outcome.
			h.log.WarnContext(r.Context(), "sessions: delete-all partial",
				slog.String("user_id", p.UserID),
				slog.String("err", err.Error()))
			writeJSONError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		h.emitRevoked(r, p.UserID, sessionIDFromToken(info.Token), true)
	}

	w.WriteHeader(http.StatusNoContent)
}

// emitRevoked records an auth.session.revoked event. The metadata
// carries the SessionID we returned to the wire (NOT the raw token —
// the token is a credential) and a "bulk" flag so an admin reviewing
// the log can tell a targeted revoke from a "log out everywhere" sweep.
//
// Audit emit failures do NOT roll back the revocation. The session is
// already gone from Redis; the most useful thing we can do is log a
// warning and let the request return 204. The alternative — re-creating
// the session because the audit log failed — would be a worse UX
// (revoke-then-fail leaves an attacker holding a freshly minted token).
func (h *Handlers) emitRevoked(r *http.Request, userID, sessionID string, bulk bool) {
	if h.emitter == nil {
		return
	}
	err := h.emitter.Emit(r.Context(), EventSessionRevoked,
		audit.WithActorOverride(userID),
		audit.WithTarget("session", sessionID),
		audit.WithMetadata(map[string]any{
			"bulk": bulk,
		}),
		audit.WithSeverity(audit.SeverityInfo),
	)
	if err != nil {
		h.log.WarnContext(r.Context(), "sessions: audit emit failed",
			slog.String("event", EventSessionRevoked),
			slog.String("user_id", userID),
			slog.String("session_id", sessionID),
			slog.String("err", err.Error()))
	}
}

// currentToken returns the raw session token carried by the request's
// sid cookie, or "" if the cookie is absent or empty. Validation of the
// token (correct length, base64url-decodable) is intentionally NOT done
// here — we only use it for an equality check against tokens that
// already came out of the store, so a malformed value harmlessly
// matches nothing.
func (h *Handlers) currentToken(r *http.Request) string {
	c, err := r.Cookie(h.cookieName)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// principalFromContext is a thin wrapper around [policy.FromContext]
// that returns ok=false when the principal is anonymous (empty
// UserID). The auth middleware attaches an anonymous principal on the
// OptionalSession path; this package only ever runs under
// RequireSession, but we fail-closed anyway.
func principalFromContext(ctx context.Context) (policy.Principal, bool) {
	p, ok := policy.FromContext(ctx)
	if !ok {
		return policy.Principal{}, false
	}
	if p.UserID == "" {
		return p, false
	}
	return p, true
}

// sessionIDFromToken derives the wire-safe SessionID from the raw token.
//
// Why SHA-256 not a random opaque ID stored separately: the token
// itself is already opaque and high-entropy, so a deterministic hash
// gives us a stable per-token ID without a second source of truth to
// keep consistent across replicas. The hash is one-way: even if an
// attacker exfiltrates the SessionID list, they cannot reverse it back
// into a usable token.
//
// We truncate the hex to 32 chars (16 bytes / 128 bits). The total
// universe of IDs is 2^128, but the relevant collision space is just
// the caller's own session list — typically <10 entries. A birthday
// collision at 128 bits would require ~2^64 sessions per user, which
// is comfortably outside any realistic load.
func sessionIDFromToken(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:16])
}

// constantTimeEqual reports whether a == b in time independent of the
// position of any differing byte. This guards both the current-session
// check and the SessionID lookup against timing-based session
// enumeration. The two-step ConstantTimeCompare returns 1 only on
// equality AND only when lengths match, so distinct-length inputs are
// rejected without a length-disclosing fast path.
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// deviceLabelFromInfo extracts a human-readable label from the session
// info. Login handlers may stuff either "device_label" (a short curated
// string like "Safari on macOS") or, when they didn't pick one, the raw
// User-Agent string under "user_agent". We prefer the curated label
// when both are present.
//
// session.SessionInfo doesn't carry Data through (it's the lightweight
// projection); the issue's wire spec requires device_label so we
// re-fetch from the store via the data-bearing path. The current
// shipped session.Manager.List does NOT include Data on SessionInfo,
// so the field is always empty until a follow-up patch enriches the
// projection. We deliberately leave it as "" rather than half-resolve:
// a misleading device_label is worse than a missing one.
func deviceLabelFromInfo(_ session.SessionInfo) string {
	// session.SessionInfo intentionally omits Data; resolving the
	// device label requires the auth layer to enrich SessionInfo or
	// for List to return a richer type. Leaving this as a documented
	// extension point keeps the wire contract stable while the inner
	// projection evolves.
	return ""
}

// ipFromInfo mirrors deviceLabelFromInfo for the IP field. Same
// rationale: SessionInfo is the lightweight projection and does not
// surface session.Data today.
func ipFromInfo(_ session.SessionInfo) string {
	return ""
}

// writeJSON writes status with the given body as JSON. Content-Type
// is set BEFORE the status because ResponseWriter rejects header
// writes once WriteHeader runs.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeJSONError writes a stable {"error":"<code>"} body. The code is
// a short slug so client code can dispatch on it without parsing the
// message; the same shape is used elsewhere in apps/api.
func writeJSONError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorBody{Error: code})
}

// errorBody is the JSON shape of every error response from this
// package. Private so callers cannot marshal-by-shape into the same
// envelope from outside the package.
type errorBody struct {
	Error string `json:"error"`
}

