package dev

import (
	"crypto/subtle"
	"net/http"
)

// headerDevToken is the request header the `gonext plugin dev` CLI sets
// on every upload. It carries the shared secret operators put in
// Config.Plugins.DevToken; the handler compares this value to its
// configured token using a constant-time check.
//
// The header name is non-canonical-but-fine for the Go net/http header
// canonicalisation (it normalises to "Dev-Token"); we declare the value
// in canonical form so the constant matches what Header.Get returns.
const headerDevToken = "Dev-Token"

// authMiddleware guards the dev-install endpoint. It rejects any
// request whose Dev-Token header does not match the configured token,
// AND any request when the configured token is empty.
//
// Empty-token rejection is intentional. A developer who turns DevMode
// on but forgets to set DevToken would otherwise expose an
// unauthenticated install endpoint to anything that can reach the
// process. Failing closed in that configuration is the safe default.
//
// The comparison uses subtle.ConstantTimeCompare so a timing oracle
// cannot leak the configured token byte-by-byte. crypto/subtle is the
// standard idiom here.
func authMiddleware(expected string) func(http.Handler) http.Handler {
	expectedBytes := []byte(expected)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Empty configured token => the handler is open to nobody.
			// We refuse every request rather than allow the absurd
			// "no auth required" fallback an empty string might suggest.
			if len(expectedBytes) == 0 {
				writeError(w, http.StatusUnauthorized, codeUnauthorized,
					"dev install endpoint requires Plugins.DevToken to be configured")
				return
			}

			got := r.Header.Get(headerDevToken)
			if got == "" {
				writeError(w, http.StatusUnauthorized, codeUnauthorized,
					"missing Dev-Token header")
				return
			}

			// Constant-time compare requires equal-length slices to
			// produce the safe answer. We pad/truncate by routing
			// mismatched lengths to an explicit failure before the
			// compare, but still call ConstantTimeCompare on a
			// same-length zero buffer so this branch executes a
			// real-shape compare too (defense-in-depth against a
			// future inversion of the if-statement).
			gotBytes := []byte(got)
			if len(gotBytes) != len(expectedBytes) {
				// Burn the compare so the timing of the wrong-length
				// path is indistinguishable from the wrong-value path.
				_ = subtle.ConstantTimeCompare(expectedBytes, expectedBytes)
				writeError(w, http.StatusUnauthorized, codeUnauthorized,
					"invalid Dev-Token")
				return
			}
			if subtle.ConstantTimeCompare(gotBytes, expectedBytes) != 1 {
				writeError(w, http.StatusUnauthorized, codeUnauthorized,
					"invalid Dev-Token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
