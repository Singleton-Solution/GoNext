// Package magiclink implements the passwordless "email me a sign-in
// link" login flow (issue #203).
//
// The flow is two HTTP endpoints:
//
//	POST /api/v1/auth/magic-link/request {email}
//	GET  /api/v1/auth/magic-link?token=<hex>
//
// # Posture
//
// The request endpoint is intentionally enumeration-safe: it always
// returns 200 regardless of whether the email is registered. An
// attacker probing for "is this address a user?" learns nothing from
// the response. The token is issued + emailed only when a user exists,
// but the wire response is identical either way.
//
// Tokens are 32 random bytes (256 bits) hex-encoded into a 64-character
// URL-safe string. Plaintext lives only in the email body and in
// volatile memory at issue time; what's persisted is SHA-256(plaintext).
// A database dump cannot be exchanged for a working magic link.
//
// Tokens are single-use and time-bounded:
//
//   - TTL: 15 minutes from issuance ([DefaultTTL]). Tighter than
//     password reset because a magic link IS the credential — once
//     consumed it mints a real session, so the abuse window must be
//     short.
//   - used_at: once the verify handler succeeds, the row is marked
//     consumed and any replay returns "invalid_or_expired_token".
//
// Consuming a magic link mints a fresh session via the deployment's
// standard [session.Manager.Create] call — same TTL, same idle window,
// same cookie attributes as a password login. Magic-link auth is one
// path to a normal session, not a separate lower-trust mode.
//
// # Rate limit
//
// The request endpoint is rate-limited per-IP (5 attempts / 15 minutes
// by default). Beyond that the limiter returns 429 with a Retry-After
// hint. The limit is keyed only on IP, not on email — rate-limiting by
// email is what attackers want, because it lets them enumerate "this
// email has been requested" via 429 vs 200 timing.
//
// The verify endpoint is NOT rate-limited per se: token possession is
// the credential, and a brute-force search over the 256-bit token
// space is computationally infeasible. Failed lookups are still audited
// so a spike is visible to operators.
//
// # Wiring
//
// One handler is constructed per process and mounted via
// [Handler.Routes]. Production wiring lives in apps/api/cmd/server/main.go
// alongside the existing auth wiring; tests in this package use the
// in-memory token + user fakes.
package magiclink
