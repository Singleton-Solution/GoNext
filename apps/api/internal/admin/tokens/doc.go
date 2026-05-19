// Package tokens implements the REST surface for Personal Access Tokens
// — the /me/tokens routes operators use to issue, list, and revoke
// long-lived API credentials.
//
// The routes live under /api/v1/me/tokens (NOT /api/v1/admin/...) for
// two reasons:
//
//   1. PATs are owned by their issuing user, even when that user is an
//      operator. The "admin" namespace is for cross-user surfaces (DLQ,
//      RUM, status); per-user tokens belong under /me/.
//   2. Routing under /me/ makes the access pattern uniform with the
//      existing /me/sessions surface (PR #291) — every operator and
//      every regular user lands on the same path.
//
// Auth contract:
//
//   - All three routes require an authenticated session OR a PAT with
//     the implicit "self" scope. A PAT can manage its sibling PATs of
//     the same user; this is intentional, matching the OAuth-app
//     posture most CIs expect.
//   - Route-level capability checks are explicit: the PAT middleware
//     and policy.Require compose with each other; see scopes.go.
//
// Response shape:
//
//   - GET /me/tokens — list of TokenView (no hash, no plaintext).
//   - POST /me/tokens — single IssuedTokenView, exactly ONCE in this
//     response; the plaintext field is the only place the operator
//     will ever see the full token. The UI is responsible for
//     surfacing the "save now, you won't see it again" warning.
//   - DELETE /me/tokens/{id} — 204 No Content.
//
// Errors use the router.ProblemDetails envelope to match the rest of
// the admin REST surface.
package tokens
