// Package impersonate is the super_admin "sign in as user" surface.
// Issue #211.
//
// Routes (mounted under base, typically /api/v1/admin/users):
//
//	POST {base}/{id}/impersonate — mint a session as the target user
//
// A companion route is mounted under /api/v1/auth/impersonation:
//
//	GET    /api/v1/auth/impersonation — surface the banner state (is the
//	                                    current session impersonated? if
//	                                    so, who is the actor?)
//	DELETE /api/v1/auth/impersonation — exit impersonation; tears down
//	                                    the impersonated session and
//	                                    rewrites the cookie back to the
//	                                    actor's original token.
//
// The handler is gated to the [policy.RoleSuperAdmin] role — operators
// at that tier are already trusted to read every secret in the system,
// so the additional "wear another user's hat" surface doesn't expand
// their blast radius. Audit emits an `admin.impersonation.started`
// event with both the actor (the super_admin) and the target user
// pinned, so the audit log later answers "who acted as user X between
// time A and time B".
//
// The minted session carries an `impersonation` flag in its data so
// the admin shell can render the "Signed in as X on behalf of Y" banner
// and offer an Exit affordance. The original session is NOT torn down
// — the banner's Exit affordance restores the operator to it.
package impersonate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/router"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// EventImpersonationStarted is the audit event type emitted for every
// successful impersonation.
const EventImpersonationStarted = "admin.impersonation.started"

// SessionMinter is the slice of [session.Manager] this package needs.
type SessionMinter interface {
	Create(ctx context.Context, userID string, data map[string]any, ttl, idleTTL time.Duration) (string, error)
}

// SessionReader is the read surface used by the banner / exit
// endpoints. Implementations typically wrap [session.Manager].
type SessionReader interface {
	// Get fetches a session by raw token. Returns ErrNotFound when
	// the token is unknown or expired.
	Get(ctx context.Context, token string, idleTTL time.Duration) (session.Session, error)
}

// SessionDeleter is the surface used to tear down an impersonated
// session on Exit.
type SessionDeleter interface {
	Delete(ctx context.Context, token string) error
}

// UserLookup returns true iff the target user exists. Implementations
// typically wrap a users.Store; tests can pass an inline closure.
type UserLookup func(ctx context.Context, userID uuid.UUID) (exists bool, err error)

// AuditEmitter is the audit surface this package depends on.
type AuditEmitter interface {
	Emit(ctx context.Context, eventType string, opts ...audit.EmitOption) error
}

// Deps is the dependency bag for [Mount].
type Deps struct {
	Sessions   SessionMinter
	Reader     SessionReader
	Deleter    SessionDeleter
	Policy     policy.Policy
	Audit      AuditEmitter
	Logger     *slog.Logger
	UserLookup UserLookup
	// TTL is the absolute lifetime of the impersonated session.
	// Defaults to 30 minutes when zero — impersonation is a heightened
	// privilege and shouldn't outlive the operator's coffee.
	TTL time.Duration
	// IdleTTL is the rolling-idle window. Defaults to 15 minutes when
	// zero.
	IdleTTL time.Duration
	// CookieName overrides the session cookie name written on the
	// response. Defaults to [session.CookieName].
	CookieName string
	// CookieSecure controls the Secure attribute on the cookie. Set
	// true in production over HTTPS.
	CookieSecure bool
}

func (d Deps) validate() error {
	if d.Sessions == nil {
		return errors.New("impersonate: Deps.Sessions is required")
	}
	if d.Policy == nil {
		return errors.New("impersonate: Deps.Policy is required")
	}
	if d.Audit == nil {
		return errors.New("impersonate: Deps.Audit is required")
	}
	return nil
}

type handlers struct {
	deps Deps
	log  *slog.Logger
}

// Mount wires the impersonation route onto mux. Gate is enforced by
// the handler itself rather than a [policy.Require] gate so we can
// require a specific role (super_admin) rather than a capability.
func Mount(mux *http.ServeMux, base string, deps Deps) error {
	if err := deps.validate(); err != nil {
		return err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.TTL == 0 {
		deps.TTL = 30 * time.Minute
	}
	if deps.IdleTTL == 0 {
		deps.IdleTTL = 15 * time.Minute
	}
	if deps.CookieName == "" {
		deps.CookieName = session.CookieName
	}
	h := &handlers{deps: deps, log: deps.Logger}
	base = strings.TrimRight(base, "/")
	mux.Handle("POST "+base+"/{id}/impersonate", http.HandlerFunc(h.start))
	return nil
}

// MountBanner wires the banner-state endpoints under the auth tree:
//
//	GET    /api/v1/auth/impersonation — surface banner state
//	DELETE /api/v1/auth/impersonation — exit impersonation
//
// authBase is typically "/api/v1/auth/impersonation". Both handlers
// require Reader and Deleter to be set on Deps.
func MountBanner(mux *http.ServeMux, authBase string, deps Deps) error {
	if deps.Reader == nil {
		return errors.New("impersonate: MountBanner requires Deps.Reader")
	}
	if deps.Deleter == nil {
		return errors.New("impersonate: MountBanner requires Deps.Deleter")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.CookieName == "" {
		deps.CookieName = session.CookieName
	}
	if deps.IdleTTL == 0 {
		deps.IdleTTL = 15 * time.Minute
	}
	h := &handlers{deps: deps, log: deps.Logger}
	authBase = strings.TrimRight(authBase, "/")
	mux.Handle("GET "+authBase, http.HandlerFunc(h.whoami))
	mux.Handle("DELETE "+authBase, http.HandlerFunc(h.exit))
	return nil
}

// whoami surfaces the banner state for the current session. Returns
// {"impersonation": false} for a normal session and {"impersonation":
// true, "actor_user_id": "...", "target_user_id": "..."} when the
// current session is an impersonation.
func (h *handlers) whoami(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(h.deps.CookieName)
	if err != nil || c == nil || c.Value == "" {
		router.WriteJSON(w, http.StatusOK, map[string]any{"impersonation": false})
		return
	}
	sess, err := h.deps.Reader.Get(r.Context(), c.Value, h.deps.IdleTTL)
	if err != nil {
		router.WriteJSON(w, http.StatusOK, map[string]any{"impersonation": false})
		return
	}
	imp, _ := sess.Data["impersonation"].(bool)
	if !imp {
		router.WriteJSON(w, http.StatusOK, map[string]any{"impersonation": false})
		return
	}
	actor, _ := sess.Data["actor_user_id"].(string)
	router.WriteJSON(w, http.StatusOK, map[string]any{
		"impersonation":  true,
		"actor_user_id":  actor,
		"target_user_id": sess.UserID,
	})
}

// exit tears down the impersonated session and rewrites the cookie
// back to the actor's original token. If the session in question
// isn't actually an impersonation, this is a no-op (idempotent).
func (h *handlers) exit(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(h.deps.CookieName)
	if err != nil || c == nil || c.Value == "" {
		router.WriteError(w, http.StatusUnauthorized, "no_session", "no session cookie")
		return
	}
	sess, err := h.deps.Reader.Get(r.Context(), c.Value, h.deps.IdleTTL)
	if err != nil {
		router.WriteError(w, http.StatusUnauthorized, "no_session", "session not found")
		return
	}
	imp, _ := sess.Data["impersonation"].(bool)
	if !imp {
		// Not impersonating; clear the response without tearing down
		// the regular session.
		router.WriteJSON(w, http.StatusOK, map[string]any{"exited": false})
		return
	}
	originalToken, _ := sess.Data["original_token"].(string)
	// Tear down the impersonated session.
	if err := h.deps.Deleter.Delete(r.Context(), c.Value); err != nil {
		h.log.WarnContext(r.Context(), "impersonate: delete failed", slog.Any("err", err))
	}
	// Restore the original cookie. If no original_token was recorded
	// (e.g. operator impersonated from a fresh tab), the cookie is
	// cleared and the operator must log in again — fail-closed.
	if originalToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     h.deps.CookieName,
			Value:    originalToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   h.deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
	} else {
		http.SetCookie(w, &http.Cookie{
			Name:     h.deps.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   h.deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
	router.WriteJSON(w, http.StatusOK, map[string]any{"exited": true})
}

// start is the POST handler. Auth path:
//
//  1. The caller MUST be authenticated (principal on context).
//  2. The caller MUST carry the super_admin role. Anything less and
//     we return 403 — a regular admin can NOT impersonate.
//  3. The target user MUST exist (else 404). The UserLookup is
//     optional; if not supplied, we skip the existence check and
//     trust the caller — useful for tests but operators should always
//     wire one in.
//  4. We mint a session as the target user, with metadata carrying
//     the original operator's user ID and a true `impersonation` flag.
//  5. We emit `admin.impersonation.started` to the audit log, pinning
//     both actor (super_admin) and target.
//  6. We Set-Cookie the new session token on the response, which
//     swaps the browser to the impersonated session on the next
//     request. The operator's original cookie value is recorded in
//     the response payload so the admin UI can restore it on Exit.
func (h *handlers) start(w http.ResponseWriter, r *http.Request) {
	p, ok := policy.FromContext(r.Context())
	if !ok || p.UserID == "" {
		router.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !hasSuperAdmin(p) {
		router.WriteError(w, http.StatusForbidden, "forbidden", "super_admin required")
		return
	}
	targetID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		router.WriteError(w, http.StatusBadRequest, "invalid_id", "id is not a valid uuid")
		return
	}
	if h.deps.UserLookup != nil {
		ok, err := h.deps.UserLookup(r.Context(), targetID)
		if err != nil {
			h.log.ErrorContext(r.Context(), "impersonate: user lookup", slog.Any("err", err))
			router.WriteError(w, http.StatusInternalServerError, "internal_error", "user lookup failed")
			return
		}
		if !ok {
			router.WriteError(w, http.StatusNotFound, "not_found", "user does not exist")
			return
		}
	}

	// Record the original session token so the banner's Exit
	// affordance can restore it. The cookie value is the raw token;
	// we read it once and stuff it in the new session's data.
	var originalToken string
	if c, err := r.Cookie(h.deps.CookieName); err == nil && c != nil {
		originalToken = c.Value
	}

	data := map[string]any{
		"impersonation":   true,
		"actor_user_id":   p.UserID,
		"original_token":  originalToken,
		"impersonated_at": time.Now().UTC().Format(time.RFC3339),
	}
	token, err := h.deps.Sessions.Create(r.Context(), targetID.String(), data, h.deps.TTL, h.deps.IdleTTL)
	if err != nil {
		h.log.ErrorContext(r.Context(), "impersonate: session create", slog.Any("err", err))
		router.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to mint session")
		return
	}

	// Emit audit. Failures here do NOT roll back the session — the
	// audit log entry is best-effort, and a missing entry is better
	// than refusing the operator's request after they've already been
	// switched.
	if err := h.deps.Audit.Emit(r.Context(), EventImpersonationStarted,
		audit.WithActorOverride(p.UserID),
		audit.WithTarget("user", targetID.String()),
		audit.WithMetadata(map[string]any{
			"impersonator_user_id": p.UserID,
			"target_user_id":       targetID.String(),
		}),
		audit.WithSeverity(audit.SeverityWarning),
	); err != nil {
		h.log.WarnContext(r.Context(), "impersonate: audit emit failed",
			slog.String("event", EventImpersonationStarted),
			slog.Any("err", err))
	}

	// Set the impersonated session cookie. The browser will use this
	// for subsequent requests; the original cookie value is encoded in
	// the response body for the Exit flow.
	http.SetCookie(w, &http.Cookie{
		Name:     h.deps.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.deps.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.deps.TTL.Seconds()),
	})

	router.WriteJSON(w, http.StatusOK, map[string]any{
		"impersonated_user_id": targetID.String(),
		"actor_user_id":        p.UserID,
		"expires_in_seconds":   int(h.deps.TTL.Seconds()),
	})
}

// hasSuperAdmin reports whether the principal carries the super_admin
// role.
func hasSuperAdmin(p policy.Principal) bool {
	for _, r := range p.Roles {
		if r == policy.RoleSuperAdmin {
			return true
		}
	}
	return false
}

// EncodeResponse is exported so the admin UI's E2E tests can decode
// the JSON shape from a recorded fixture without dragging in the
// internal map literal. Returns the canonical JSON bytes for a
// success response.
func EncodeResponse(actorID, targetID string, ttl time.Duration) []byte {
	out, _ := json.Marshal(map[string]any{
		"impersonated_user_id": targetID,
		"actor_user_id":        actorID,
		"expires_in_seconds":   int(ttl.Seconds()),
	})
	return out
}
