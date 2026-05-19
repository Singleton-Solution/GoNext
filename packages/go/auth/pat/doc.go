// Package pat implements Personal Access Tokens — long-lived bearer
// credentials for programmatic API access (CI, CLI, external scripts).
//
// PATs are deliberately NOT sessions. They live in their own table
// (000024_personal_access_tokens), ride in the Authorization header
// instead of a cookie, never rotate, and carry an explicit scope list
// that the middleware intersects with the issuing user's effective
// capabilities at every request — the narrower of the two wins.
//
// Wire format
//
// A token is the string "gnp_" followed by 32 base62 characters from
// crypto/rand. The "gnp_" namespace lets log scrubbers and secret-
// scanners pattern-match a leaked credential without false positives
// against arbitrary base62. The plaintext is only ever returned by
// Store.Issue, once, at creation. The database stores the argon2id
// PHC hash of the full plaintext; lookup recomputes the hash and
// compares in constant time.
//
// Why argon2id and not SHA-256?
//
// Sessions hash with SHA-256 because the input is a 256-bit token from
// crypto/rand — brute force is already infeasible without a memory-
// hard KDF. PATs use argon2id with the same parameters as user
// passwords to defend against the case where an operator names a
// token after the secret (e.g. "my-prod-stripe-key-2026") and a
// dump leak gives an adversary a head start. The cost is one extra
// argon2 verify per request, which the middleware caches against the
// (user_id, request) tuple — we don't recompute on every header read.
//
// Scope intersection
//
// The token row carries a TEXT[] of capability slugs. At authn time
// the middleware builds a Principal whose role set is empty and whose
// CapabilitySet is exactly `scopes ∩ user-caps`. A token issued with
// scope "posts.read" cannot do "posts.write" even if the user's roles
// would normally allow it. The intersection means revoking a user's
// admin role automatically defangs their CI token without touching
// the token table.
//
// See migrations/000024_personal_access_tokens.up.sql for the storage
// model and the design notes that pin every column to a non-goal.
package pat
