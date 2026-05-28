package pat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// AuditEmitter is the slice of audit.Emitter we depend on. The real
// emitter satisfies it; tests pass a no-op fake. Accepting an interface
// here lets the audit chain change shape (e.g. wrap with tracing)
// without touching this package.
type AuditEmitter interface {
	Emit(ctx context.Context, eventType string, opts ...audit.EmitOption) error
}

// Deps is the dependency bag for Mount. Every field's zero value is
// either invalid or has a sensible default; Mount documents which.
type Deps struct {
	// Pool is the Postgres connection pool the store talks to. Required.
	Pool *pgxpool.Pool

	// Pepper is the secret HMAC'd into the password-hash input via
	// packages/go/auth/password. Required. May be empty in tests but
	// production wiring should always pass cfg.Auth.Pepper bytes.
	Pepper []byte

	// AuditEmitter receives auth.pat.* events (created / revoked).
	// Optional — nil disables audit emission. Production wiring should
	// always pass the live emitter.
	AuditEmitter AuditEmitter

	// Logger receives structured log lines. nil falls back to
	// slog.Default — fine for tests, but production wiring should
	// always pass a service logger.
	Logger *slog.Logger
}

// Audit event types emitted by this package. Exported so admin tooling
// and integration tests can compare without re-typing the magic string.
const (
	EventTokenCreated = "auth.pat.created"
	EventTokenRevoked = "auth.pat.revoked"
)

// handlers is the resolved-Deps form passed around inside the package.
// Constructed by Mount; never used standalone.
type handlers struct {
	store   *Store
	audit   AuditEmitter
	logger  *slog.Logger
	now     func() time.Time
}

// Mount wires the /me/tokens routes onto mux under base (canonically
// "/api/v1/me/tokens"). Returns an error rather than panicking if
// Deps is malformed so the caller's boot path can warn-and-continue.
//
// The route tree:
//
//	GET    {base}        — list current user's active tokens
//	POST   {base}        — issue; response carries plaintext ONCE
//	DELETE {base}/{id}   — revoke (idempotent; second call is 204)
//
// Every route requires the user to be authenticated (a Principal on
// the request context, populated upstream by RequireSession or the
// bearer middleware). Mount itself does NOT add a session wrapper —
// the caller's main.go is the right place to compose that, mirroring
// how /api/v1/auth/sessions wires itself.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if mux == nil {
		return errors.New("pat: mux is required")
	}
	if deps.Pool == nil {
		return errors.New("pat: Pool is required")
	}
	if len(deps.Pepper) == 0 {
		return errors.New("pat: Pepper is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := &handlers{
		store:  NewStore(deps.Pool, deps.Pepper),
		audit:  deps.AuditEmitter,
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
	base = strings.TrimRight(base, "/")
	mux.Handle("GET "+base, h.gate(h.list))
	mux.Handle("POST "+base, h.gate(h.create))
	mux.Handle("DELETE "+base+"/{id}", h.gate(h.revoke))
	return nil
}

// gate ensures a Principal is on the context. It does NOT do a
// capability check — every authenticated user may manage their own
// tokens. Cross-user surfaces (admin managing OTHER users' tokens)
// live at /api/v1/admin/users/{id}/tokens and gate with policy.Require.
func (h *handlers) gate(next func(http.ResponseWriter, *http.Request, policy.Principal)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr, ok := policy.FromContext(r.Context())
		if !ok || pr.UserID == "" {
			router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r, pr)
	})
}

// TokenView is the on-wire shape returned by list and revoke. It is
// deliberately separate from Token (the SQL scan target) so the hash
// never appears in JSON even if a future refactor adds a Hash field
// to Token. Keep this in lock-step with apps/admin/.../tokens/types.ts.
type TokenView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// IssuedTokenView extends TokenView with the plaintext bearer the
// operator must save. The Plaintext field is the only place the
// operator will ever see the full token; subsequent List calls only
// show Prefix. The SaveNow flag is a UI hint that the plaintext is
// non-recoverable — admin UIs SHOULD render an unmissable warning.
type IssuedTokenView struct {
	TokenView
	Token   string `json:"token"`
	SaveNow bool   `json:"save_now"`
}

func toView(t Token) TokenView {
	scopes := t.Scopes
	if scopes == nil {
		// Keep the JSON encoding stable: a token with zero scopes
		// serialises as "[]", never "null". The admin UI ranges over
		// the field unconditionally.
		scopes = []string{}
	}
	return TokenView{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		Scopes:     append([]string{}, scopes...),
		CreatedAt:  t.CreatedAt,
		LastUsedAt: t.LastUsedAt,
		ExpiresAt:  t.ExpiresAt,
	}
}

// list handles GET /me/tokens. Returns the active tokens for the
// caller, never another user's. The store enforces user_id scoping
// at the SQL level so a bug here cannot widen the audience.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	rows, err := h.store.List(r.Context(), pr.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth/pat: list failed",
			slog.String("user_id", pr.UserID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to list tokens")
		return
	}
	out := make([]TokenView, 0, len(rows))
	for _, t := range rows {
		out = append(out, toView(t))
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

// CreateRequest is the JSON body for POST /me/tokens.
type CreateRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresIn is the duration before the token expires, expressed
	// as one of: "30d", "90d", "1y", "never". The presets match the
	// admin UI radio group. Empty string means "never".
	ExpiresIn string `json:"expires_in"`
}

// presetExpiry maps the preset string to a duration. Unknown presets
// return ok=false; the handler maps that to a 400.
//
// Keep the preset list in lock-step with apps/admin/.../tokens/types.ts
// (ExpiresPreset). Adding a new preset requires both ends.
func presetExpiry(s string, now time.Time) (*time.Time, bool) {
	switch strings.TrimSpace(s) {
	case "", "never":
		return nil, true
	case "30d":
		t := now.Add(30 * 24 * time.Hour)
		return &t, true
	case "90d":
		t := now.Add(90 * 24 * time.Hour)
		return &t, true
	case "1y":
		t := now.Add(365 * 24 * time.Hour)
		return &t, true
	default:
		return nil, false
	}
}

// create handles POST /me/tokens. On success returns 201 with the
// IssuedTokenView — the ONLY place the operator will ever see the
// plaintext.
func (h *handlers) create(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	var req CreateRequest
	dec := json.NewDecoder(r.Body)
	// Strict decode: an extra field is a client bug we want to catch
	// at integration time, not silently swallow.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		router.WriteError(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	}
	expiresAt, ok := presetExpiry(req.ExpiresIn, h.now())
	if !ok {
		router.WriteError(w, http.StatusBadRequest, "invalid_expires_in", "expires_in must be one of: never, 30d, 90d, 1y")
		return
	}
	// Dedup scopes — a multi-select UI can submit duplicates after a
	// double-click. The store also strips empties; we dedup here so
	// the response shape matches what the caller asked for.
	req.Scopes = dedup(req.Scopes)

	created, err := h.store.Create(r.Context(), CreateInput{
		UserID:    pr.UserID,
		Name:      req.Name,
		Scopes:    req.Scopes,
		ExpiresAt: expiresAt,
	})
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, ErrInvalidName):
		router.WriteError(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	case errors.Is(err, ErrInvalidUserID):
		router.WriteError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	default:
		h.logger.ErrorContext(r.Context(), "auth/pat: create failed",
			slog.String("user_id", pr.UserID),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}

	h.emitAudit(r, pr.UserID, EventTokenCreated, created.Token.ID, map[string]any{
		"name":   created.Token.Name,
		"prefix": created.Token.Prefix,
		"scopes": created.Token.Scopes,
	})

	view := IssuedTokenView{
		TokenView: toView(created.Token),
		Token:     created.Plaintext,
		SaveNow:   true,
	}
	router.WriteJSON(w, http.StatusCreated, view)
}

// revoke handles DELETE /me/tokens/{id}. ErrNotFound from the store
// covers both "no such id" and "id belongs to another user" — we map
// both to 404 to avoid the existence oracle a 403 would surface.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request, pr policy.Principal) {
	id := r.PathValue("id")
	if id == "" {
		// Structurally unreachable under ServeMux's "/{id}" pattern,
		// but the off-route call path used by tests can hit it.
		router.WriteError(w, http.StatusNotFound, "not_found", "token not found")
		return
	}
	err := h.store.Revoke(r.Context(), pr.UserID, id)
	switch {
	case err == nil:
		h.emitAudit(r, pr.UserID, EventTokenRevoked, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		router.WriteError(w, http.StatusNotFound, "not_found", "token not found")
	default:
		h.logger.ErrorContext(r.Context(), "auth/pat: revoke failed",
			slog.String("user_id", pr.UserID),
			slog.String("token_id", id),
			slog.Any("err", err),
		)
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to revoke token")
	}
}

// emitAudit records an auth.pat.* event. Errors are non-fatal — the
// state change has already landed in the DB by the time we get here,
// and rolling back because the audit log failed would be a worse
// outcome (operator stuck without a token, or unable to revoke a
// credential they think is dead).
func (h *handlers) emitAudit(r *http.Request, userID, eventType, tokenID string, meta map[string]any) {
	if h.audit == nil {
		return
	}
	opts := []audit.EmitOption{
		audit.WithActorOverride(userID),
		audit.WithTarget("personal_access_token", tokenID),
		audit.WithSeverity(audit.SeverityInfo),
	}
	if meta != nil {
		opts = append(opts, audit.WithMetadata(meta))
	}
	if err := h.audit.Emit(r.Context(), eventType, opts...); err != nil {
		h.logger.WarnContext(r.Context(), "auth/pat: audit emit failed",
			slog.String("event", eventType),
			slog.String("user_id", userID),
			slog.String("token_id", tokenID),
			slog.Any("err", err),
		)
	}
}

// dedup trims and removes duplicate strings, preserving first-seen
// order. Used on the scopes slice; a multi-select UI can submit dupes
// after a double-click. Trimming + skipping empties means " read " and
// "read" collapse to the single canonical "read".
func dedup(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
