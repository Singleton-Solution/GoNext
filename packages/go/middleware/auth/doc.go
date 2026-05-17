// Package auth is the GoNext HTTP middleware that wires the session
// store and the policy engine into request handling. It is the bridge
// between the opaque session cookie shipped by the browser and the
// [policy.Principal] that downstream handlers expect on the context.
//
// # Middlewares
//
// Three middlewares cover the common authn/authz shapes:
//
//   - [RequireSession]: enforce that the request carries a valid session
//     cookie. On success the [policy.Principal] derived from the session
//     is attached to the context (via [policy.WithPrincipal]) and the
//     request-scoped logger gets a user_id field (via [log.WithRequest])
//     so every downstream log line is correlated. On failure (missing
//     cookie, malformed token, expired session) the middleware writes a
//     401 with a clean JSON body and does NOT call next.
//
//   - [OptionalSession]: identical to RequireSession on the happy path,
//     but on missing/invalid session it falls through to next with an
//     anonymous principal (UserID == "", no roles) on the context. Use
//     this for public pages that opportunistically personalize when a
//     user is signed in.
//
//   - [RequireCapability]: route-level capability gate. Reads the
//     principal off the context (set by RequireSession) and consults the
//     policy. Returns 401 JSON when no principal is present (i.e. the
//     middleware was used without RequireSession in front of it), 403
//     JSON when the principal lacks the capability. Mirrors
//     [policy.Require] but with the JSON error shape and the
//     [httpx.Middleware] return type the rest of the chassis expects.
//
// # Wiring
//
// In a typical handler chain (outermost first):
//
//	httpx.Chain(handler,
//	    httpx.Recovery(logger),
//	    httpx.RequestID(),
//	    httpx.Logger(logger),
//	    auth.RequireSession(sessionMgr),
//	    auth.RequireCapability(pol, policy.CapEditPosts),
//	)
//
// RequireSession reads the session cookie set by package session
// (default cookie name "sid"), calls [session.Manager.Get] with the
// configured idle TTL, and on a valid session builds a Principal
// using the configured principal builder (see [DefaultPrincipal]).
//
// # Principal derivation
//
// The default derivation pulls UserID from [session.Session.UserID] and
// Roles from the session's Data map under the key "roles" (a JSON array
// of role slugs). Callers who store roles under a different key, or
// who want to fetch roles from a database keyed by UserID, supply a
// [PrincipalBuilder] via [WithPrincipalBuilder] when constructing the
// middleware.
//
// # Error responses
//
// 401 and 403 responses share the same JSON shape:
//
//	{"error":"unauthorized"}
//	{"error":"forbidden"}
//
// The body is deliberately uninformative: callers MUST NOT learn from
// the response whether their token was missing, malformed, expired, or
// pointed at a known-bad session — that's a useful signal for attackers
// probing the auth layer. Operators see the precise cause in the
// structured log line emitted by the [httpx.Logger] middleware.
package auth
