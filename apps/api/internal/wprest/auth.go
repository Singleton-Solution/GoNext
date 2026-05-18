package wprest

import (
	"crypto/subtle"
	"net/http"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// WP REST clients authenticate write requests using a "nonce" carried in
// the `X-WP-Nonce` header (or, for legacy fetches, the `?_wpnonce=` query
// parameter). The nonce is morally the same construct as our CSRF
// double-submit token: a short-lived, server-minted string the client
// must present on every state-changing call.
//
// Rather than build a second token-minting pipeline, the shim BRIDGES the
// nonce header to the existing CSRF cookie. The verifier runs the same
// HMAC-comparison logic as packages/go/middleware/csrf: the presented
// nonce must equal the value of the CSRF cookie issued by the same
// secret. The application wires its CSRF middleware in front of this
// surface so every reader has a cookie minted; write callers echo the
// cookie back in `X-WP-Nonce`.
//
// The bridge keeps the rotation policy identical (same TTL, same secret,
// same Same-Site cookie) so a token rotation in the admin UI invalidates
// the WP REST surface in the same heartbeat — there's no separate clock
// to maintain.

// HeaderWPNonce is the canonical header WP REST clients use to carry the
// nonce. Both header and query-parameter (?_wpnonce=) are honored; live
// WP also accepts both.
const HeaderWPNonce = "X-WP-Nonce"

// QueryParamWPNonce is the alternate query-parameter form. Older WP
// fetches (especially the @wordpress/api-fetch fallback) emit the nonce
// here when scripts can't set custom headers (e.g. on a cross-subdomain
// `<form>` post).
const QueryParamWPNonce = "_wpnonce"

// CookieName is the name of the CSRF cookie the shim reads to verify the
// presented nonce. Must match the production CSRF middleware's
// Options.CookieName (defaults to "csrf").
const CookieName = "csrf"

// NonceVerifier resolves a request to "the nonce is valid" or "rejected".
//
// The default implementation (defaultNonceVerifier) bridges to the CSRF
// cookie issued by packages/go/middleware/csrf. Tests substitute a
// fake verifier so they can exercise the policy gate independent of
// CSRF wiring. The interface is intentionally tiny — verification is the
// only operation; minting is the CSRF middleware's job.
type NonceVerifier interface {
	// Verify returns nil when the request carries a valid nonce, or a
	// non-nil error describing the failure. Callers translate any
	// non-nil error to a 403 `rest_cookie_invalid_nonce`.
	Verify(r *http.Request) error
}

// defaultNonceVerifier is the bridge to the CSRF cookie. It does the
// minimum work to satisfy the WP semantics:
//
//  1. extract the presented nonce from header-then-query
//  2. extract the cookie from the named cookie jar entry
//  3. constant-time compare the two
//
// The defaultNonceVerifier does NOT re-verify the HMAC on the cookie —
// the CSRF middleware does that on every state-changing request and
// would have already rejected a forged cookie before we got here. This
// is the "morally same as CSRF" property: we delegate cryptographic
// freshness to the CSRF middleware and ride on top of its decision.
type defaultNonceVerifier struct {
	cookieName string
}

// NewBridgedNonceVerifier returns the default verifier wired to the
// given cookie name. Pass the same string the production CSRF
// middleware uses for its Options.CookieName.
func NewBridgedNonceVerifier(cookieName string) NonceVerifier {
	if cookieName == "" {
		cookieName = CookieName
	}
	return &defaultNonceVerifier{cookieName: cookieName}
}

// errNonceMissing / errNonceMismatch are the two non-success outcomes
// the verifier can return. The handler maps both to the WP error
// `rest_cookie_invalid_nonce` so we don't leak detail — but tests can
// switch on the sentinel to distinguish "didn't send" from "wrong value".
var (
	errNonceMissing  = &nonceError{code: "missing"}
	errNonceMismatch = &nonceError{code: "mismatch"}
)

// nonceError is a tiny sentinel type. Using a custom type (rather than
// errors.New) lets tests pattern-match on the error code without string
// inspection.
type nonceError struct {
	code string
}

func (e *nonceError) Error() string { return "wprest: nonce " + e.code }

// Verify implements NonceVerifier by comparing the presented nonce to
// the CSRF cookie value, in constant time. A missing-cookie or
// missing-header case is treated as a missing-nonce error; a present
// header that doesn't match is a mismatch.
func (v *defaultNonceVerifier) Verify(r *http.Request) error {
	presented := r.Header.Get(HeaderWPNonce)
	if presented == "" {
		// Fall back to the query-string form. WP clients running outside
		// a JS fetch (form posts, server-side scrapers) typically use
		// the parameter; we accept either.
		presented = r.URL.Query().Get(QueryParamWPNonce)
	}
	if presented == "" {
		return errNonceMissing
	}

	cookie, err := r.Cookie(v.cookieName)
	if err != nil || cookie.Value == "" {
		return errNonceMissing
	}

	// Constant-time compare avoids a timing oracle when an attacker can
	// see response timing and is iterating on candidate nonces. The CSRF
	// cookie value is a fixed-size base64url token from mintToken; if
	// the lengths mismatch ConstantTimeCompare returns 0, which we treat
	// as a mismatch.
	if subtle.ConstantTimeCompare([]byte(presented), []byte(cookie.Value)) != 1 {
		return errNonceMismatch
	}
	return nil
}

// requireNonce is the write-path entry guard: if the verifier rejects
// the request, we emit the canonical WP error and return false (caller
// should stop processing). When the shim's Deps.NonceVerifier is nil
// (the test mode), the gate is open.
//
// Returning a bool keeps the handler body flat — caller checks the
// boolean and bails on false rather than threading a sentinel through
// each path.
func (h *handlers) requireNonce(w http.ResponseWriter, r *http.Request) bool {
	if h.deps.NonceVerifier == nil {
		return true
	}
	if err := h.deps.NonceVerifier.Verify(r); err != nil {
		writeError(w, http.StatusForbidden, errCodeInvalidNonce,
			"Cookie check failed")
		return false
	}
	return true
}

// requirePrincipal extracts the authenticated principal from the
// request context. Returns the principal + true on success; on failure,
// writes a WP-shaped 401 and returns false. The principal is required
// for capability checks AND for audit emission (the actor user id is
// always the principal's UserID; we never emit anonymous writes).
//
// When Deps.PrincipalFromContext is set (production wiring), we use it.
// In tests the field is nil and we fall through to policy.FromContext,
// which is the package-default lookup the auth middleware populates.
func (h *handlers) requirePrincipal(w http.ResponseWriter, r *http.Request) (policy.Principal, bool) {
	var pr policy.Principal
	var ok bool
	if h.deps.PrincipalFromContext != nil {
		pr, ok = h.deps.PrincipalFromContext(r.Context())
	} else {
		pr, ok = policy.FromContext(r.Context())
	}
	if !ok {
		// The CSRF/nonce check has already passed if we got here, so
		// the right WP code is "logged out", not "invalid nonce".
		writeError(w, http.StatusUnauthorized, errCodeUnauthenticated,
			"You are not currently logged in.")
		return policy.Principal{}, false
	}
	return pr, true
}

// requireCapability runs a policy check; writes a WP 403 on denial and
// returns false. Used for both route-level gates (edit_posts to POST
// /posts) and object-level ones (delete_others_posts on a row whose
// author is not the principal).
func (h *handlers) requireCapability(w http.ResponseWriter, pr policy.Principal, cap policy.Capability, resource any) bool {
	if h.deps.Policy == nil {
		// No policy wired (tests sometimes mount without one). Open the
		// gate — the test that exercises capability denials wires a
		// real policy.
		return true
	}
	d := h.deps.Policy.Can(pr, cap, resource)
	if !d.Allowed {
		writeError(w, http.StatusForbidden, errCodeForbidden,
			"Sorry, you are not allowed to do that.")
		return false
	}
	return true
}
