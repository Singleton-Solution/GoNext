package passwordreset

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy width of one reset token. 32 bytes (256
// bits) matches the email-verify token width — at this width brute
// force against a single token is infeasible for any plausible threat
// model. The hex encoding produces 64 characters, which fits in any
// URL path or query parameter without escaping.
const tokenBytes = 32

// hexLen is the length of a hex-encoded token. Used by validToken
// to reject obviously-malformed input before hitting the database.
const hexLen = tokenBytes * 2

// generateToken returns a fresh 256-bit token hex-encoded. The hex
// form is URL-safe and human-pasteable — relevant because users will
// occasionally copy a reset link from their email client into a
// browser by hand.
//
// crypto/rand is used directly because a predictable reset token is a
// one-click account-takeover primitive.
func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("passwordreset: read entropy: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// hashToken returns the hex-encoded SHA-256 of the supplied plaintext
// token. The hex form is stored in password_reset_tokens.token_hash so
// anyone with database read access cannot exchange the stored value
// for a working token — they'd need a 256-bit preimage of an
// already-random secret.
//
// SHA-256 is sufficient here without salting because the input is
// itself a 256-bit random secret. The usual reasons we salt (defeat
// rainbow tables of low-entropy human inputs) do not apply.
func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// constantTimeEqual reports whether two strings are equal in constant
// time. Defense-in-depth against timing attacks on the hash compare —
// the storage lookup already uses the hash as a key, so the exposed
// surface is small, but the cost is one allocation per request and the
// hygiene is cheap to keep.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// validToken reports whether s could be a token this package issued —
// the exact hex length and only hex characters. Cheap pre-check that
// keeps obviously-malformed input off the database round-trip.
func validToken(s string) bool {
	if len(s) != hexLen {
		return false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return false
	}
	return true
}
