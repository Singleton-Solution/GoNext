// Package csrf is a GoNext HTTP middleware that protects state-changing
// requests against Cross-Site Request Forgery using the double-submit
// cookie pattern combined with HMAC-signed, time-bounded tokens.
//
// # Design
//
// The middleware sits in the chain (typically after RequestID, after the
// session/auth middleware) and applies these rules:
//
//   - Any request whose URL path is not canonical (contains "..", "./",
//     "//" etc.) is rejected with 400 Bad Request BEFORE the SkipPaths
//     or method check. This closes a class of bypasses where a request
//     like POST /webhooks/../admin/users would match a /webhooks/ skip
//     prefix here but be canonicalized to /admin/users by the
//     downstream router (Go's http.ServeMux, chi, gorilla/mux).
//
//   - Safe HTTP methods (GET, HEAD, OPTIONS) are never blocked. A request
//     of these methods is allowed through; if no CSRF cookie is set yet,
//     one is issued on the response so the very next state-changing call
//     from the same client has a token to echo back.
//
//   - State-changing methods (POST, PUT, PATCH, DELETE) must present a
//     token in EITHER the X-CSRF-Token request header (the SPA flow) OR
//     the csrf_token form field (the classic HTML-form flow). The header
//     name and form field are configurable.
//
//   - The presented token is compared against the CSRF cookie using
//     crypto/subtle.ConstantTimeCompare (timing-safe). A mismatch is a
//     hard 403.
//
//   - Tokens are HMAC-SHA256 of (cookieID + ":" + unix-timestamp-seconds)
//     keyed by the provided secret, base64url-encoded along with the
//     plaintext timestamp so the server can verify freshness without
//     server-side state. Tokens older than TTL (default 1h) are rejected
//     with 403 even if the HMAC verifies — this caps the blast radius of
//     a stolen cookie value.
//
//   - SkipPaths is consulted after the canonical-path check; matching
//     prefixes (e.g. /auth/login, /webhooks/) bypass CSRF entirely. The
//     token cookie is still issued on a skipped GET so subsequent admin
//     calls have one.
//
// # Cookie attributes
//
// The CSRF cookie is issued with:
//
//   - SameSite=Lax — blocks cross-origin form POSTs and most CSRF vectors
//     while keeping top-level navigation working.
//   - Secure — set automatically when the request is TLS (r.TLS != nil),
//     or when Options.TrustProxyHeaders is true and the request carries
//     X-Forwarded-Proto: https. Without TrustProxyHeaders the middleware
//     ignores the forwarded header (otherwise an attacker on a
//     non-TLS-terminating deployment could set the header to force
//     Secure and silently DOS the user when the browser drops the
//     cookie on the plain-HTTP origin).
//   - Path=/ — applies to every route on the host.
//   - NOT HttpOnly — the admin JS must read the cookie to copy it into
//     X-CSRF-Token. This is the trade-off of double-submit; the secret
//     is the server's HMAC key, not the cookie value.
//
// # Body consumption
//
// For Content-Type: application/x-www-form-urlencoded requests, the
// middleware reads at most Options.MaxFormBodyBytes (default 64 KiB) of
// the body, parses it to extract the CSRF token, and restores r.Body
// via bytes.NewReader so downstream handlers can re-read it. For
// multipart/form-data the body is consumed by ParseMultipartForm and
// the parsed values remain accessible via r.PostFormValue/r.FormValue.
//
// # Public API
//
//	mw := csrf.New([]byte(cfg.Auth.CSRFSecret), csrf.Options{
//	    TTL:               time.Hour,
//	    SkipPaths:         []string{"/auth/login", "/auth/oidc/callback", "/webhooks/"},
//	    TrustProxyHeaders: cfg.Server.TLSTerminatedUpstream,
//	    MaxFormBodyBytes:  256 << 10, // bump for endpoints with large form bodies
//	})
//	handler = mw(handler)
//
//	// In a template handler:
//	token := csrf.Token(r) // pulls the cookie value (already minted by mw)
//	// emit <input type="hidden" name="csrf_token" value="{{ .Token }}">
//	// or for SPAs, the JS reads document.cookie and sends X-CSRF-Token.
//
// # References
//
//   - docs/06-auth-permissions.md §9 (CSRF Protection)
//   - docs/13-security-baseline.md §10 (CSRF cross-cutting)
//   - OWASP Double Submit Cookie pattern
//   - RFC 6265 §4.1.2.2 (Max-Age cookie attribute semantics)
package csrf
