// Package verify implements the HTTP flow for email-address verification.
//
// The flow is the textbook double-opt-in pattern: an authenticated user
// asks the server to send them a confirmation link, the server stores a
// short-lived secret keyed by the user, and a GET against the link
// proves possession of the secret and marks the email verified.
//
// # Endpoints
//
//   - POST /api/v1/auth/verify/send — authenticated. Generates a
//     32-byte random token, persists its SHA-256 hash in Redis under
//     a key with a 24-hour TTL, and dispatches an email containing
//     the verify link. Returns 202 Accepted with an empty body.
//     Rate-limited to 1 send per minute per authenticated user.
//
//   - GET /api/v1/auth/verify?token=<plain> — anonymous. Hashes the
//     supplied token, looks it up in Redis, and on a match updates
//     users.email_verified_at = now() then deletes the token. The
//     plaintext token is compared against the stored hash with a
//     constant-time hash compare, so an attacker who can submit
//     candidate tokens cannot use timing to narrow the search space.
//     Returns 200 {"verified": true} on success, 410 Gone on an
//     expired or unknown token.
//
// # Why Redis instead of a SQL table
//
// The token is single-use, short-lived, and high-cardinality (one row
// per pending verification). Redis offers native TTL expiry that
// matches the 24-hour deadline exactly — no janitor job, no
// "deleted_at" tombstone, no missed-index cost on a write-heavy table.
// The relational analogue would be an `email_verifications` table; we
// reserve the name for future flows that need a longer history
// (anti-spam tally per email, e.g.), but the verification token
// itself fits Redis perfectly.
//
// # Key layout
//
//	email_verify:{sha256_hex(token)}  →  user_id  (TTL = 24h)
//
// The token is base64url(32 random bytes), so the wire form is 43
// printable characters. The Redis key is the hex-encoded SHA-256 of
// the token, so anyone with Redis read access cannot exchange the
// stored key for a working verification — they'd have to brute-force
// the preimage of 256 bits of entropy, which is infeasible.
//
// # Audit
//
//   - auth.verify.email.sent — emitted from POST send when the email
//     has been queued. SeverityInfo. Metadata.recipient = the masked
//     email address (local-part trimmed) so an audit reader can spot
//     a flurry of sends without leaking the full address.
//
//   - auth.verify.email.completed — emitted from GET verify when the
//     UPDATE succeeded. SeverityInfo.
//
//   - auth.verify.email.invalid — emitted from GET verify when the
//     token did not match. SeverityWarning so SOC dashboards can spot
//     scanners.
//
// # Why 410 Gone for invalid tokens
//
// 410 communicates "the resource was here and is no longer available"
// — the right semantics for a one-shot link that has already been
// consumed or has expired. 401 / 403 imply the user is anonymous,
// which is a less precise mental model for an unauthenticated
// endpoint that simply doesn't have the right secret.
package verify
