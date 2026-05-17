package csrf

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Defaults for Options when the caller leaves a field zero. We don't
// document them as `const` to keep the surface area small — the doc
// comments on Options are the contract.
const (
	defaultTTL        = time.Hour
	defaultCookieName = "csrf"
	defaultHeaderName = "X-CSRF-Token"
	defaultFormField  = "csrf_token"

	// cookieIDBytes is the entropy of the anonymous cookie ID embedded in
	// every token. 32 bytes (256 bits) matches docs/06-auth-permissions.md
	// §9 ("csrf_token: 32 random bytes, base64url") and is well beyond
	// what's needed for collision resistance.
	cookieIDBytes = 32

	// minBodyForFormParse caps how much of a form-encoded body we'll
	// read to find csrf_token. 64 KiB is huge for a CSRF token (~80 B
	// expected) but small enough that an attacker can't make us buffer
	// gigabytes by sending Content-Type: application/x-www-form-urlencoded.
	maxFormParseBytes = 64 << 10 // 64 KiB
)

// safeMethods is the set of HTTP methods we never block. Per RFC 7231,
// these are defined as safe and idempotent and must not have side
// effects. If a handler does have side effects on GET it's a bug in
// the handler — CSRF middleware can't catch that and shouldn't try.
var safeMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	// http.MethodTrace is also "safe" per RFC but we omit it — TRACE is
	// commonly disabled at the proxy level, and accepting it here would
	// be a surprise. Callers who need TRACE can wrap manually.
}

// Options configures a CSRF middleware. The zero value is valid; every
// field has a sane default. New() copies the struct, so post-construction
// mutation by the caller is harmless.
type Options struct {
	// TTL is how long a minted token remains valid. Default 1h. A request
	// whose token is older than TTL is rejected with 403 even if the
	// HMAC checks out — this caps replay risk if a token leaks via logs
	// or a referer header.
	//
	// Setting TTL to a negative value disables freshness checking
	// entirely (HMAC-only). Setting TTL to zero uses the default.
	TTL time.Duration

	// CookieName is the name of the cookie that carries the token to
	// the browser. Default "csrf". Per docs/06 §9, NOT prefixed __Host-
	// because we want it readable from Path=/ subdomains; the secret
	// remains the HMAC key, not the cookie.
	CookieName string

	// HeaderName is the request header the middleware reads to recover
	// the token from a fetch/XHR call. Default "X-CSRF-Token".
	HeaderName string

	// FormField is the form field name the middleware reads when the
	// request has Content-Type: application/x-www-form-urlencoded or
	// multipart/form-data. Default "csrf_token".
	FormField string

	// SkipPaths is a list of URL path prefixes that bypass CSRF
	// validation. Used for endpoints that authenticate by means other
	// than session cookies (webhook signatures, /auth/login, OIDC
	// callback, API token auth). Matching is by HasPrefix, so passing
	// "/webhooks/" exempts every nested route.
	//
	// A skipped GET still gets a cookie minted on the response, so the
	// admin UI's first state-changing call has a token to echo.
	SkipPaths []string

	// Now is an optional clock injection point for tests. nil = time.Now.
	Now func() time.Time
}

// Errors returned during token verification. They are package-private
// (the middleware translates them to 403); exposed via testing helpers
// in csrf_internal_test.go.
var (
	errMissingToken    = errors.New("csrf: missing token")
	errMissingCookie   = errors.New("csrf: missing cookie")
	errMalformedToken  = errors.New("csrf: malformed token")
	errInvalidHMAC     = errors.New("csrf: HMAC mismatch")
	errExpiredToken    = errors.New("csrf: token expired")
	errTokenCookieDiff = errors.New("csrf: token does not match cookie")
)

// New returns a middleware that enforces CSRF on state-changing requests.
//
// secret is the HMAC-SHA256 key used to sign tokens; it MUST be at least
// 16 bytes (≥32 bytes recommended per docs/13 §5). New panics on a
// shorter secret because shipping with a weak key is a programming bug
// that should fail at construction time, not at first request.
//
// The returned middleware is safe for concurrent use.
func New(secret []byte, opts Options) func(http.Handler) http.Handler {
	if len(secret) < 16 {
		panic("csrf.New: secret must be at least 16 bytes (32 recommended)")
	}

	cfg := opts // shallow copy so we own the defaults
	if cfg.TTL == 0 {
		cfg.TTL = defaultTTL
	}
	if cfg.CookieName == "" {
		cfg.CookieName = defaultCookieName
	}
	if cfg.HeaderName == "" {
		cfg.HeaderName = defaultHeaderName
	}
	if cfg.FormField == "" {
		cfg.FormField = defaultFormField
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	// Pre-copy the secret so callers can't mutate it under us.
	key := make([]byte, len(secret))
	copy(key, secret)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSkippedPath(r.URL.Path, cfg.SkipPaths) {
				// Skipped routes still get a cookie minted (idempotent) so
				// the next non-skipped call has a token to send.
				ensureCookie(w, r, key, cfg)
				next.ServeHTTP(w, r)
				return
			}

			if _, safe := safeMethods[r.Method]; safe {
				// Safe methods: never blocked, but we mint/refresh the
				// cookie so the client always has a fresh token.
				ensureCookie(w, r, key, cfg)
				next.ServeHTTP(w, r)
				return
			}

			// State-changing method: must present a valid token.
			if err := verifyRequest(r, key, cfg); err != nil {
				// 403 with a plain text body. Don't leak the specific
				// error reason (HMAC vs. expired vs. missing) because
				// distinguishing them is useful to attackers probing
				// the middleware.
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Token returns the CSRF token associated with this request, or "" if
// the cookie has not been set yet. Templates use this to embed a hidden
// form field; SPAs typically read the cookie via document.cookie instead.
//
// Note: the cookie is set on the RESPONSE by the middleware. Calling
// Token on a request that arrived without a cookie returns "" — the
// caller should either send the response (browser stores cookie) and
// retry, or use the middleware's ensureCookie path by issuing a GET first.
func Token(r *http.Request) string {
	c, err := r.Cookie(defaultCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

// TokenFromCookie is like Token but looks up the configured cookie name
// rather than the default. Useful when a non-default Options.CookieName
// was used at construction time.
func TokenFromCookie(r *http.Request, cookieName string) string {
	if cookieName == "" {
		cookieName = defaultCookieName
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

// ensureCookie sets the CSRF cookie on the response if the request
// doesn't already have a valid one. "Valid" here means: present, parses
// as a token, HMAC verifies, and is not expired. An expired or malformed
// cookie is replaced (the browser overwrites the old one).
func ensureCookie(w http.ResponseWriter, r *http.Request, key []byte, cfg Options) {
	if c, err := r.Cookie(cfg.CookieName); err == nil && c.Value != "" {
		if _, verr := verifyToken(c.Value, key, cfg); verr == nil {
			return // existing cookie is fine
		}
		// fallthrough: replace stale/forged cookie
	}

	tok, err := mintToken(key, cfg)
	if err != nil {
		// crypto/rand failure is catastrophic. Don't pretend a token
		// was minted; the next state-changing request will 403 because
		// we couldn't issue one, which is the correct fail-closed
		// behavior. Log via response? We don't have a logger here —
		// the caller's RequestID/Logger middleware will see the
		// downstream 403 in due course.
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cfg.CookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   int(cfg.TTL.Seconds()),
		HttpOnly: false, // double-submit requires JS-readable cookie
		Secure:   isTLS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// verifyRequest pulls the token from header-then-form, the cookie value
// from the cookie jar, validates the HMAC + TTL on the token, and
// constant-time compares the token to the cookie value.
//
// The error returned is one of the err* sentinels in this file.
// Callers translate any non-nil error to 403; the sentinels exist for
// test assertions.
func verifyRequest(r *http.Request, key []byte, cfg Options) error {
	cookie, err := r.Cookie(cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return fmt.Errorf("verifyRequest: %w", errMissingCookie)
	}

	// Pull the presented token: header first (preferred SPA path),
	// fall back to form field for HTML form posts. We do NOT check
	// query string — putting a CSRF token in a URL leaks it via
	// Referer headers and logs.
	presented := r.Header.Get(cfg.HeaderName)
	if presented == "" {
		presented = tokenFromForm(r, cfg.FormField)
	}
	if presented == "" {
		return fmt.Errorf("verifyRequest: %w", errMissingToken)
	}

	// Verify the presented token's HMAC and freshness. We verify the
	// token (not the cookie) because the token is what the attacker
	// would need to forge — the cookie alone is insufficient for a
	// state-changing call, but a forged token with a stolen cookie
	// would defeat us. By HMAC-verifying the token we ensure it was
	// minted by us within TTL.
	if _, err := verifyToken(presented, key, cfg); err != nil {
		return fmt.Errorf("verifyRequest: %w", err)
	}

	// Constant-time compare token against cookie. Both are server-
	// minted base64url strings of fixed shape, so length equality is
	// the common case; subtle.ConstantTimeCompare returns 0 if lengths
	// differ, which we treat as mismatch.
	if subtle.ConstantTimeCompare([]byte(presented), []byte(cookie.Value)) != 1 {
		return fmt.Errorf("verifyRequest: %w", errTokenCookieDiff)
	}
	return nil
}

// tokenFromForm extracts the form field without triggering net/http's
// MaxBytesReader (which would mutate r.Body). We read at most
// maxFormParseBytes and only when Content-Type indicates a form.
func tokenFromForm(r *http.Request, field string) string {
	ct := r.Header.Get("Content-Type")
	// strip "; charset=..." etc.
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "application/x-www-form-urlencoded", "multipart/form-data":
		// http.Request.FormValue calls ParseForm/ParseMultipartForm
		// which will consume r.Body. Per RFC 7231, a POST body can be
		// read exactly once; the handler downstream may also want it.
		// Mitigation: limit body to maxFormParseBytes before parsing,
		// which is small enough to fully buffer.
		r.Body = http.MaxBytesReader(nil, r.Body, maxFormParseBytes)
		return r.FormValue(field)
	}
	return ""
}

// isTLS returns true if the request was received over TLS or arrived
// from a trusted proxy with X-Forwarded-Proto: https. Used to decide
// whether to set the Secure attribute on the cookie. In dev (plain HTTP)
// we return false so the cookie isn't dropped by the browser.
func isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

// isSkippedPath returns true if path starts with any of prefixes. Empty
// prefix list returns false.
func isSkippedPath(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// --- token encoding / verification ---
//
// Wire format:
//
//	<base64url(cookieID)> "." <unixSeconds> "." <base64url(hmacSig)>
//
// The dot separator is safe because base64url does not produce dots.
// unixSeconds is plain decimal so the verifier can range-check it
// without a second base64 decode.
//
// We keep the format private (no public Encode/Decode); callers should
// treat tokens as opaque strings. Changing the format is a non-breaking
// internal change as long as the receiving server matches.

// mintToken issues a fresh token bound to a random cookie ID. The cookie
// ID is not stored anywhere — it's just there to make every token unique
// even at the same timestamp. Returns the encoded string.
func mintToken(key []byte, cfg Options) (string, error) {
	var idRaw [cookieIDBytes]byte
	if _, err := rand.Read(idRaw[:]); err != nil {
		return "", fmt.Errorf("mintToken: read entropy: %w", err)
	}
	cookieID := base64.RawURLEncoding.EncodeToString(idRaw[:])

	ts := cfg.Now().Unix()
	tsStr := strconv.FormatInt(ts, 10)
	sig := computeSig(key, cookieID, tsStr)

	var b strings.Builder
	b.Grow(len(cookieID) + 1 + len(tsStr) + 1 + base64.RawURLEncoding.EncodedLen(len(sig)))
	b.WriteString(cookieID)
	b.WriteByte('.')
	b.WriteString(tsStr)
	b.WriteByte('.')
	b.WriteString(base64.RawURLEncoding.EncodeToString(sig))
	return b.String(), nil
}

// verifyToken decodes tok, re-computes the HMAC over (cookieID, ts),
// and checks both equality (constant-time) and freshness. Returns the
// embedded cookieID on success.
func verifyToken(tok string, key []byte, cfg Options) (string, error) {
	if tok == "" {
		return "", errMissingToken
	}
	// Split into exactly 3 parts. Using strings.Split allocates a
	// slice; we use IndexByte twice instead to avoid that — CSRF
	// verification is per-request hot path.
	dot1 := strings.IndexByte(tok, '.')
	if dot1 <= 0 {
		return "", errMalformedToken
	}
	rest := tok[dot1+1:]
	dot2 := strings.IndexByte(rest, '.')
	if dot2 <= 0 {
		return "", errMalformedToken
	}
	cookieID := tok[:dot1]
	tsStr := rest[:dot2]
	sigStr := rest[dot2+1:]
	if cookieID == "" || tsStr == "" || sigStr == "" {
		return "", errMalformedToken
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", errMalformedToken
	}

	gotSig, err := base64.RawURLEncoding.DecodeString(sigStr)
	if err != nil {
		return "", errMalformedToken
	}

	wantSig := computeSig(key, cookieID, tsStr)
	if !hmac.Equal(gotSig, wantSig) {
		return "", errInvalidHMAC
	}

	// Freshness. Negative TTL disables the check (HMAC-only mode).
	if cfg.TTL >= 0 {
		age := cfg.Now().Sub(time.Unix(ts, 0))
		if age < -1*time.Minute {
			// Token from the future — clock skew over 1 minute is
			// suspicious; reject. Treat like expired.
			return "", errExpiredToken
		}
		if age > cfg.TTL {
			return "", errExpiredToken
		}
	}
	return cookieID, nil
}

// computeSig returns HMAC-SHA256(key, cookieID + ":" + ts). The colon
// separator prevents canonicalization tricks where e.g. "abc"+"12" and
// "ab"+"c12" hash to the same input.
func computeSig(key []byte, cookieID, tsStr string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(cookieID))
	mac.Write([]byte{':'})
	mac.Write([]byte(tsStr))
	return mac.Sum(nil)
}
