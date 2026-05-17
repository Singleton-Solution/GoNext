// Package password hashes user passwords with argon2id and an HMAC-bound
// server-side pepper.
//
// The scheme has two layers:
//
//  1. A pre-hash: HMAC-SHA256(pepper, password). The pepper is a 32-byte
//     server-side secret loaded from GONEXT_AUTH_PEPPER. Mixing the pepper
//     in here — rather than concatenating it with the salt — binds the
//     pepper to the input and means a stolen database alone is not enough
//     to attempt an offline crack: the attacker also needs the pepper.
//
//  2. argon2id over the pre-hash output. Defaults follow RFC 9106:
//     memory = 64 MiB, iterations = 3, parallelism = 2, salt = 16 bytes
//     from crypto/rand, hash length = 32 bytes.
//
// The encoded output is the standard PHC string:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<hash-b64>
//
// where salt and hash use raw (unpadded) standard base64. This is the same
// format produced by libsodium and the reference C library; any consumer
// that understands "$argon2id$" can round-trip it.
//
// Verify uses subtle.ConstantTimeCompare and returns a needsRehash flag
// when the stored hash was produced with parameters weaker than the
// current defaults (e.g. after we bump memory on faster hardware). The
// caller is expected to re-hash and persist the new encoded string on
// the next successful login.
//
// Passwords and pepper values never appear in returned errors — error
// strings describe the failure shape (malformed encoding, unsupported
// version, etc.) without echoing input. Empty passwords are accepted by
// Hash and Verify; policy enforcement (minimum length, weak-list checks)
// is the responsibility of the caller, not this package.
//
// See docs/06-auth-permissions.md §3 (Password Storage) and RFC 9106.
package password
