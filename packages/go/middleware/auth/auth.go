package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
)

// DefaultIdleTTL is the rolling idle window the middleware passes to
// [session.Manager.Get] when the caller did not override it with
// [WithIdleTTL]. 30 minutes matches the most common "rolling idle"
// behavior for admin UIs — it's long enough that a user reading an
// article doesn't get logged out, short enough that a stolen cookie has
// a bounded blast radius after the user walks away from their browser.
const DefaultIdleTTL = 30 * time.Minute

// DefaultCookieName is the cookie name read by the middleware. It
// matches [session.CookieName] so the default wire-up "just works".
const DefaultCookieName = session.CookieName

// SessionStore is the slice of [session.Manager] the auth middleware
// depends on. It exists so tests can substitute an in-memory fake
// without standing up Redis; *session.Manager satisfies it by virtue
// of having the right Get method shape.
//
// We deliberately do NOT pull in Create/Delete/List/Regenerate — those
// are the login-handler's job, not the request-time middleware's. A
// smaller surface keeps the test seam minimal.
type SessionStore interface {
	// Get loads the session blob keyed by token and refreshes its
	// idle TTL on success. The semantics are documented on
	// [session.Manager.Get]; see that method for the error contract
	// ([session.ErrNotFound], [session.ErrInvalidToken]).
	Get(ctx context.Context, token string, idleTTL time.Duration) (session.Session, error)
}

// PrincipalBuilder constructs a [policy.Principal] from a loaded
// session. The default ([DefaultPrincipal]) reads UserID off the
// session and Roles from the "roles" key of [session.Session.Data].
//
// Callers who store roles under a different key, who want to map a
// numeric UserID through a directory lookup, or who want to enrich the
// Principal with cached attributes pass their own builder via
// [WithPrincipalBuilder].
type PrincipalBuilder func(sess session.Session) policy.Principal

// Options configures the middleware. The zero value is valid; every
// field has a documented default. Apply options by passing them to
// [RequireSession] / [OptionalSession] as functional arguments.
type Options struct {
	// IdleTTL is the rolling idle window passed to
	// [session.Manager.Get]. Default [DefaultIdleTTL].
	IdleTTL time.Duration

	// CookieName is the name of the request cookie that carries the
	// opaque session token. Default [DefaultCookieName].
	CookieName string

	// PrincipalBuilder derives a [policy.Principal] from the loaded
	// session. Default [DefaultPrincipal].
	PrincipalBuilder PrincipalBuilder
}

// Option mutates Options. Used by [RequireSession] and
// [OptionalSession]; supplying no options keeps the defaults.
type Option func(*Options)

// WithIdleTTL overrides the rolling idle window passed to the session
// manager on each request. Must be positive; non-positive values are
// ignored and the default is kept.
func WithIdleTTL(d time.Duration) Option {
	return func(o *Options) {
		if d > 0 {
			o.IdleTTL = d
		}
	}
}

// WithCookieName overrides the request cookie name. Empty strings are
// ignored and the default is kept.
func WithCookieName(name string) Option {
	return func(o *Options) {
		if name != "" {
			o.CookieName = name
		}
	}
}

// WithPrincipalBuilder overrides the Principal derivation step. nil is
// ignored and the default is kept.
func WithPrincipalBuilder(b PrincipalBuilder) Option {
	return func(o *Options) {
		if b != nil {
			o.PrincipalBuilder = b
		}
	}
}

// defaultOptions returns a fresh Options populated with the package
// defaults. Tests use it directly; the public middlewares apply the
// caller's Options on top.
func defaultOptions() Options {
	return Options{
		IdleTTL:          DefaultIdleTTL,
		CookieName:       DefaultCookieName,
		PrincipalBuilder: DefaultPrincipal,
	}
}

// RequireSession returns middleware that enforces a valid session on
// every request. On success it attaches the derived [policy.Principal]
// to the context via [policy.WithPrincipal] AND adds the UserID to the
// request-scoped logger via [log.WithRequest], then calls next. On any
// failure (missing cookie, malformed token, expired session) it writes
// 401 with `{"error":"unauthorized"}` and does NOT call next.
//
// The middleware does NOT clear the cookie on failure — that's the
// caller's responsibility, typically by returning a 401 page that
// instructs the browser to log in. Clearing here would leak a useful
// distinction (your token was bad vs. you weren't authenticated) to
// any attacker probing the endpoint.
func RequireSession(mgr *session.Manager, opts ...Option) httpx.Middleware {
	return requireSession(mgr, opts...)
}

// requireSession is the testable seam: it accepts the smaller
// [SessionStore] interface so tests can pass a fake without standing
// up Redis. The public [RequireSession] forwards to it.
func requireSession(store SessionStore, opts ...Option) httpx.Middleware {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := loadPrincipal(r, store, cfg)
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalSession returns middleware that opportunistically loads a
// session. On success the principal + log fields are attached just
// like [RequireSession]. On any failure the request falls through to
// next with an anonymous principal on the context — UserID == "",
// no roles. Use this for routes that personalize when a user is
// signed in but render anyway when they aren't (the marketing site,
// public read-only API responses with optional user-scoped fields).
func OptionalSession(mgr *session.Manager, opts ...Option) httpx.Middleware {
	return optionalSession(mgr, opts...)
}

func optionalSession(store SessionStore, opts ...Option) httpx.Middleware {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := loadPrincipal(r, store, cfg)
			if !ok {
				// Anonymous fallthrough: attach the zero Principal so
				// downstream code can still call FromContext without
				// branching on presence.
				ctx = policy.WithPrincipal(r.Context(), AnonymousPrincipal())
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// loadPrincipal is the shared core of [RequireSession] and
// [OptionalSession]. It reads the cookie, loads the session, derives a
// Principal, and attaches both the Principal and the user_id log field
// to a derived context.
//
// ok == false means "no usable session" for any reason (missing
// cookie, invalid token, expired session, transient redis error). The
// caller decides whether that's a 401 or an anonymous fallthrough.
func loadPrincipal(r *http.Request, store SessionStore, cfg Options) (context.Context, bool) {
	ctx := r.Context()
	c, err := r.Cookie(cfg.CookieName)
	if err != nil || c.Value == "" {
		return ctx, false
	}

	sess, err := store.Get(ctx, c.Value, cfg.IdleTTL)
	if err != nil {
		// All errors from session.Manager.Get are "no usable session"
		// for our purposes — ErrNotFound and ErrInvalidToken collapse
		// to the same outcome (401 / anonymous), and a transient redis
		// error should ALSO be 401 (fail-closed) rather than letting a
		// caller through unauthenticated. The auth layer is not the
		// right place to introduce retry policy on a backing store.
		return ctx, false
	}

	p := cfg.PrincipalBuilder(sess)
	ctx = policy.WithPrincipal(ctx, p)
	// Only set the user_id log field when we actually have one. The
	// log.WithRequest call drops empty fields, but skipping the call
	// entirely on the anonymous path saves a logger.With allocation.
	if p.UserID != "" {
		ctx = log.WithRequest(ctx, log.RequestFields{UserID: p.UserID})
	}
	return ctx, true
}

// RequireCapability returns middleware that enforces a primitive
// capability before the wrapped handler runs. The principal MUST
// already be on the context (i.e. [RequireSession] runs upstream); if
// it isn't, the middleware returns 401 JSON. If the principal lacks
// the capability, the middleware returns 403 JSON.
//
// The 403 body is `{"error":"forbidden"}` — deliberately uninformative.
// The structured log line emitted by [httpx.Logger] carries the
// principal's UserID and the route path, which is enough for operators
// to diagnose without leaking decision reasons to the caller. (Compare
// [policy.Require], which writes the decision Reason into the response
// body — that's fine for an internal dashboard but too chatty for a
// public API.)
func RequireCapability(p policy.Policy, capability policy.Capability) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pr, ok := policy.FromContext(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			d := p.Can(pr, capability, nil)
			if !d.Allowed {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PrincipalFromRequest is sugar for [policy.FromContext] applied to
// the request's context. The second return reports whether a principal
// is on the context at all; the anonymous principal also returns true
// (it was put there explicitly by [OptionalSession]).
func PrincipalFromRequest(r *http.Request) policy.Principal {
	if r == nil {
		return policy.Principal{}
	}
	p, _ := policy.FromContext(r.Context())
	return p
}

// AnonymousPrincipal returns the canonical "no user" principal — empty
// UserID, no roles. [OptionalSession] attaches this when the request
// has no usable session.
//
// Returning a value (not a pointer) means callers cannot mutate the
// canonical instance.
func AnonymousPrincipal() policy.Principal {
	return policy.Principal{}
}

// DefaultPrincipal derives a [policy.Principal] from a loaded session.
// UserID maps directly; Roles are pulled from sess.Data["roles"] as a
// JSON array of strings. Unknown role slugs are passed through
// verbatim — the policy layer treats them as roles with no
// capabilities (see BasicPolicy.Can), so a stale role string in
// session data cannot grant a capability it doesn't already have.
//
// If sess.Data has no "roles" key, the resulting Principal has no
// roles. The auth layer does NOT make a database lookup here — that's
// the login handler's job to bake into the session at creation time.
// Doing the lookup on every request would turn the auth middleware
// into a cache layer.
func DefaultPrincipal(sess session.Session) policy.Principal {
	return policy.Principal{
		UserID: sess.UserID,
		Roles:  rolesFromData(sess.Data),
	}
}

// rolesFromData decodes the "roles" key of a session's Data map into a
// []policy.Role. The shape we accept is what encoding/json would
// produce from a []string: a []any of strings, since
// json.Unmarshal(..., *map[string]any) widens elements to interface{}.
//
// Any other shape (missing key, wrong type, nested objects) returns
// nil. We deliberately do NOT log here — a session with malformed
// roles data is a programmer error in the login handler, not a
// per-request signal.
func rolesFromData(data map[string]any) []policy.Role {
	if len(data) == 0 {
		return nil
	}
	raw, ok := data["roles"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]policy.Role, 0, len(v))
		for _, r := range v {
			if s, ok := r.(string); ok && s != "" {
				out = append(out, policy.Role(s))
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []string:
		// Tolerate the typed shape too: tests that build a session
		// directly (not through JSON round-trip) may pass []string.
		out := make([]policy.Role, 0, len(v))
		for _, s := range v {
			if s != "" {
				out = append(out, policy.Role(s))
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []policy.Role:
		// Already-typed roles: copy so the caller can't mutate the
		// returned slice and leak into the session blob.
		out := make([]policy.Role, len(v))
		copy(out, v)
		return out
	default:
		return nil
	}
}

// writeJSONError writes status with a {"error": msg} JSON body. We
// emit Content-Type before the status because http.ResponseWriter
// silently rejects header writes after WriteHeader.
//
// Encoding errors are unreachable for a constant-shape struct, but if
// the wire fails mid-write we cannot recover the status line — the
// client will see a truncated response, which is the best we can do.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Don't cache 401/403 responses: a CDN that caches "unauthorized"
	// would lock out legitimate users after the first probe.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: msg})
}

// errorBody is the JSON shape of every error response from this
// package. Keeping it private prevents accidental marshal-by-shape
// duplicates elsewhere in the chassis.
type errorBody struct {
	Error string `json:"error"`
}
