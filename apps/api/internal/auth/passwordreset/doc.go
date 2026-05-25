// Package passwordreset implements the "I forgot my password" recovery
// flow (issue #140).
//
// The flow is two HTTP endpoints:
//
//	POST /api/v1/auth/password-reset/request {email}
//	POST /api/v1/auth/password-reset/confirm {token, new_password}
//
// # Posture
//
// The flow is intentionally enumeration-safe: the request endpoint
// always returns 200 regardless of whether the email is registered.
// An attacker probing for "is this address a user?" learns nothing
// from the response. The same posture applies to the wire timing —
// callers should not see a measurable difference between known and
// unknown emails. The implementation issues + emails the token only
// when a user exists, but the response shape and headers are identical
// either way.
//
// Tokens are 32 random bytes (256 bits) hex-encoded into a 64-character
// URL-safe string. The plaintext appears only:
//
//   - in the email body delivered to the user, and
//   - briefly in volatile memory at issue time.
//
// What's persisted in the database is SHA-256(plaintext). A database
// dump cannot be used to forge a reset; the attacker would need a
// 256-bit preimage.
//
// Tokens are single-use and time-bounded:
//
//   - TTL: 1 hour from issuance (DefaultTTL). Matches OWASP's
//     "Password Reset Cheat Sheet" upper bound.
//   - used_at: once the confirm handler succeeds, the row is marked
//     consumed and any replay returns "invalid_or_expired_token".
//
// Confirming a reset additionally invalidates ALL active sessions
// for that user via [session.Manager.DeleteAllForUser]. This shuts
// down a stolen cookie that an attacker may have planted before the
// legitimate user noticed the compromise.
//
// # Rate limit
//
// Both endpoints share a per-IP token bucket (5 attempts per 15
// minutes by default). Beyond that the limiter returns 429 with a
// Retry-After hint. The limit is keyed only on IP, not on email —
// rate-limiting by email is what attackers want, because it lets them
// enumerate "this email has been requested" via 429 vs 200 timing.
//
// # Wiring
//
// One handler is constructed per process and mounted via
// [Handler.Routes]. Production wiring lives in apps/api/cmd/server/main.go
// alongside the existing auth wiring; tests in this package use the
// in-memory token + user fakes.
package passwordreset
