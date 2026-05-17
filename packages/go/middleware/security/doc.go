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
// CSP is intentionally NOT set here. Content-Security-Policy requires
// per-request nonce binding and per-route classification that belongs in
// its own middleware; mixing it into a generic headers middleware would
// either produce an unsafe policy or leak coupling. See docs/13 §3 for
// the dedicated CSP design.
//
// Wiring example:
//
//	srv := httpx.New(httpx.Options{
//	    Handler: mux,
//	    Middlewares: []httpx.Middleware{
//	        httpx.Recovery(logger),
//	        httpx.RequestID(),
//	        security.Headers(security.PublicSite()),
//	        httpx.Logger(logger),
//	    },
//	})
//
// See docs/13-security-baseline.md §2.1 for the canonical header matrix
// and §2.2 for the Permissions-Policy deny list.
package security
