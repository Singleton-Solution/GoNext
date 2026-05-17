// Package security provides HTTP middleware that applies the canonical
// security headers described in docs/13-security-baseline.md §2.
//
// The package exposes a single middleware constructor, Headers, plus
// preset Options builders for the common deployment shapes:
//
//   - DefaultOptions — strict defaults suitable for HTML-serving origins.
//   - PublicSite     — same as Default; named for clarity at call sites.
//   - Admin          — stricter (X-Frame-Options: DENY, COEP: require-corp).
//   - RESTAPI        — geared for JSON APIs consumed by other origins
//     (CORP: cross-origin), with the embedder/opener policies dropped
//     because they don't apply to non-document responses.
//
// Each header in the matrix can be disabled or overridden via Options.
// Headers are written before next.ServeHTTP runs so downstream handlers
// can override per-response if they truly need to (the middleware writes,
// it does not lock).
//
// In addition to writing the canonical matrix, Headers deletes the
// Server and X-Powered-By response headers (controlled by
// Options.StripIdentifyingHeaders, on by default in every preset). This
// removes a cheap fingerprinting vector that reverse proxies and Go's
// own net/http often add.
//
// Per-request CSP nonce delivery is provided by the separate WithNonce
// middleware. WithNonce generates a fresh 128-bit nonce per request from
// crypto/rand, attaches it to r.Context (retrievable via
// NonceFromContext), and writes it to the X-Script-Nonce response
// header. A downstream Next.js (or other SSR) frontend reads the header
// and stamps the nonce into inline <script nonce="..."> tags. The CSP
// policy itself is intentionally not set here — see docs/13 §3 for the
// dedicated CSP design.
//
// Route classification: callers whose mux mixes route classes (admin,
// REST, public, media, plugin-frontend) can use ClassifiedHeaders which
// uses Classify(r) to pick the appropriate Options preset per request.
// SetClassifierPrefixes and SetClassifier configure the classifier;
// OptionsFor(class) returns the corresponding preset for static wiring.
//
// Wiring example:
//
//	srv := httpx.New(httpx.Options{
//	    Handler: mux,
//	    Middlewares: []httpx.Middleware{
//	        httpx.Recovery(logger),
//	        httpx.RequestID(),
//	        security.WithNonce(),
//	        security.Headers(security.PublicSite()),
//	        httpx.Logger(logger),
//	    },
//	})
//
// See docs/13-security-baseline.md §2.1 for the canonical header matrix
// and §2.2 for the Permissions-Policy deny list.
package security
