package security

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// nonceContextKey is the private context key used to attach the per-request
// CSP nonce to the request context. Keeping the type unexported prevents
// collisions with keys from other packages.
type nonceContextKey struct{}

// NonceHeader is the name of the response header used to convey the
// per-request CSP nonce to downstream consumers (e.g. a Next.js frontend
// that needs to stamp `<script nonce="...">` tags into its server-rendered
// HTML). Exposed as a constant so callers can reference it without
// stringly-typed coupling.
const NonceHeader = "X-Script-Nonce"

// nonceByteLen is the entropy budget for each nonce. 16 bytes (128 bits)
// is the value mandated by docs/13-security-baseline.md §2.3 and matches
// the CSP3 recommendation for strict-dynamic deployments.
const nonceByteLen = 16

// generateNonce returns a fresh base64-encoded nonce drawn from
// crypto/rand. Errors from the OS RNG are surfaced rather than ignored;
// callers in WithNonce will fall back to a 500 response, since silently
// emitting a static nonce would defeat the security guarantee.
//
// Standard (padded) base64 is used because CSP3 accepts any base64 form
// in source-expression values; padding has no observable effect on
// matching. URL-safe base64 would also be valid; we keep standard for
// stability with established header value conventions.
func generateNonce() (string, error) {
	var buf [nonceByteLen]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf[:]), nil
}

// WithNonce returns a middleware that generates a fresh CSP nonce per
// request, attaches it to r.Context, and writes it to the X-Script-Nonce
// response header so downstream consumers (notably a Next.js frontend that
// renders inline <script> tags) can stamp it into their HTML.
//
// The middleware is intentionally narrow: it only mints and exposes a
// nonce. The CSP policy itself — including how to combine the nonce with
// `script-src 'nonce-...'` — is a separate concern handled by the CSP
// middleware that will be added in a follow-up. See doc 13 §3 for the
// dedicated CSP design.
//
// On RNG failure the request is short-circuited with 500 Internal Server
// Error; this is preferable to letting a request proceed without a nonce
// because downstream handlers may assume one is always present.
//
// Goroutine safety: the middleware allocates one nonce per request on the
// hot path and stores it on the request context; nothing is shared across
// requests. Safe to register globally.
//
// Wiring example:
//
//	mux := http.NewServeMux()
//	mux.Handle("/", pageHandler)
//	srv := &http.Server{
//	    Handler: security.WithNonce()(security.Headers(security.PublicSite())(mux)),
//	}
//
// A Next.js (or other SSR) frontend in front of this Go service reads the
// X-Script-Nonce response header and stamps it into rendered HTML, e.g.
// <script nonce="{nonce}">…</script>.
func WithNonce() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce, err := generateNonce()
			if err != nil {
				http.Error(w, "nonce generation failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set(NonceHeader, nonce)
			ctx := context.WithValue(r.Context(), nonceContextKey{}, nonce)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// NonceFromContext returns the per-request CSP nonce stored on ctx by
// WithNonce, or the empty string if no nonce is attached.
//
// Empty-string sentinel (rather than (string, bool)) keeps call sites
// terse for the common path (template helpers, header builders).
// Callers that need to distinguish "no middleware" from "RNG returned ”"
// can compare against the empty string explicitly; generateNonce never
// returns an empty success value.
func NonceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(nonceContextKey{}).(string); ok {
		return v
	}
	return ""
}
