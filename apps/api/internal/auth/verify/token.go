package verify

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy width of one verification token. 32 bytes
// (256 bits) is the same width [session] uses for its session
// identifiers — it makes a brute-force attack against a single token
// infeasible for any plausible threat model.
const tokenBytes = 32

// generateToken returns a fresh 256-bit token in base64url (no
// padding). The returned string is URL-safe and fits in a query
// parameter without escaping.
//
// The function uses crypto/rand directly because a predictable
// verification token is a one-click takeover primitive.
func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("verify: read entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashToken returns the hex-encoded SHA-256 of the supplied plaintext
// token. The hex form is used as the Redis key suffix so anyone with
// Redis read access cannot exchange the stored key for a working
// verification — they'd need a 256-bit preimage.
//
// SHA-256 is fine here (no salting needed) because the input is itself
// a high-entropy random secret: the usual reasons we salt — to defeat
// rainbow tables of low-entropy human inputs — do not apply.
func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// constantTimeEqual reports whether two strings are equal in constant
// time. Used when comparing the supplied token's hash against the
// stored hash; subtle.ConstantTimeCompare guards the negative path
// from leaking length-mismatch information through CPU timing.
//
// Strictly speaking, the Redis key lookup already hashes the input,
// so the surface for a timing attack is small. We use this anyway
// because the cost is one allocation per request and the cryptographic
// hygiene is worth more than that.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// validToken reports whether s could be a token this package issued.
// It guards against feeding garbage to Redis as a key suffix and
// against trivial probes. It does NOT prove the token is live — only
// that the shape is right.
func validToken(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	if _, err := base64.RawURLEncoding.DecodeString(s); err != nil {
		return false
	}
	return true
}
