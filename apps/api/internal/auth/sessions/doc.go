// Package sessions implements the "Where you're logged in" HTTP surface.
//
// The package owns three endpoints, all authenticated:
//
//   - GET    /api/v1/auth/sessions       — list the caller's live sessions.
//   - DELETE /api/v1/auth/sessions/{id}  — revoke one specific session.
//   - DELETE /api/v1/auth/sessions       — revoke every session except the
//     one this request is using ("log me out everywhere else").
//
// Wiring is the caller's responsibility: build a [Handlers] with a live
// [session.Manager] and [audit.Emitter], then mount [Handlers.Routes]
// behind the [auth.RequireSession] middleware on the router of choice.
//
// # Session identity on the wire
//
// The raw session token is a credential equivalent to the cookie value —
// it must never be returned to the browser as a stable identifier the UI
// can later send back. This package therefore exposes each session via a
// short stable [SessionID] derived from a SHA-256 of the raw token; the
// raw token never leaves the package. Lookup by [SessionID] is O(n) over
// the user's session list, which is fine: the cardinality is bounded by
// the per-user session ceiling (a small two-digit number in practice).
//
// # Current-session semantics
//
// The "current" flag in the list response and the skip-current behavior
// of [Handlers.DeleteAll] are computed from the sid cookie carried by the
// request — the same cookie [auth.RequireSession] already validated
// upstream. Reading the cookie a second time here is cheap and keeps the
// handler self-contained (no new context key needs to be threaded through
// the middleware chain).
//
// # Audit trail
//
// Every successful revocation — single or bulk — emits exactly one
// audit.session.revoked event per session removed. The list endpoint
// emits nothing: reads are not auditable events under §13's scope.
package sessions
