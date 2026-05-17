package session

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the size of the raw entropy backing every session
// token. 32 bytes = 256 bits, the same width Google, AWS, and OWASP
// recommend for opaque session identifiers.
const tokenBytes = 32

// generateToken returns a fresh 256-bit token, base64url-encoded
// without padding. The encoded form is URL-safe and cookie-safe.
//
// The function uses crypto/rand directly — never math/rand — because a
// predictable session token is a session-hijacking primitive.
func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("session: read entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validToken reports whether s looks like one of our tokens. It guards
// against feeding garbage to Redis as a key and against trivial probes
// (e.g. ".." or "*"). It does NOT prove the token is live — only the
// shape is right.
func validToken(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	if _, err := base64.RawURLEncoding.DecodeString(s); err != nil {
		return false
	}
	return true
}
