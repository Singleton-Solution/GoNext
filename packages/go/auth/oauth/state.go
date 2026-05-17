package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
)

// stateEntropyBytes is the size of the random buffer used to mint a state
// value. 32 bytes = 256 bits — well above the OAuth2 spec recommendation
// of 128 bits, and aligned with the entropy budget for session tokens.
const stateEntropyBytes = 32

// DefaultStateTTL is the recommended lifetime of a state entry: 10 minutes
// is generous enough to survive a slow IdP page or a user hesitating at the
// consent screen, and tight enough that a stolen state value has a small
// window of utility. Callers may pass a different TTL to StateStore.Put for
// providers that warrant a different bound (e.g., GitHub device-code flow
// should use a longer TTL).
const DefaultStateTTL = 10 * time.Minute

// State generates a cryptographically random state value suitable for the
// OAuth2 state parameter.
//
// The output is base64url-encoded (no padding) and is URL-safe with no
// further escaping. Entropy is ≥256 bits. Errors are propagated only when
// crypto/rand fails, which on a healthy system means the kernel CSPRNG is
// broken — a condition the caller should not paper over.
//
// Callers MUST store the state via StateStore.Put before redirecting the
// browser, and MUST consume it via StateStore.Get on the callback. The
// state alone is not a session token; it is the CSRF defense for the
// authorization-code flow.
func State() (string, error) {
	buf := make([]byte, stateEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure is catastrophic — surface it loudly. The
		// caller almost certainly wants to fail the whole login attempt.
		return "", fmt.Errorf("oauth: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Nonce generates a cryptographically random nonce suitable for the OIDC
// nonce claim. It uses the same entropy budget and encoding as State so
// the two are interchangeable on the wire — but they have different
// semantics: state ties the callback to the original browser, nonce ties
// the ID token to the authorization request.
//
// A separate function is offered (rather than reusing State()) because
// callers commonly need both at once and naming them distinctly makes the
// audit trail more readable.
func Nonce() (string, error) {
	buf := make([]byte, stateEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: generate nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// StateData is the payload a StateStore holds for each issued state
// value.
//
// RedirectURI is the URL that was passed to Provider.AuthURL. On the
// callback the caller compares it to the URL the IdP redirected to,
// closing a class of mix-up attacks where a stolen authorization code
// is replayed against a different redirect URI.
//
// ExpectedNonce is the OIDC nonce the IdP must echo back in the ID
// token. The provider implementation (genericOIDC) checks it via
// go-oidc's IDTokenVerifier; callers using a non-OIDC provider may
// leave it empty.
//
// PKCEVerifier holds the PKCE code_verifier when the provider opted into
// PKCE. The verifier is sensitive (it proves possession of the
// authorization-request initiator) and so MUST NOT be logged or
// surfaced in errors.
//
// CreatedAt is the wall-clock moment the entry was persisted. A store
// implementation may use it to enforce TTLs idempotently — even if the
// caller passes a wildly large TTL, an implementation may cap the
// effective lifetime at the install-wide maximum.
type StateData struct {
	RedirectURI   string
	ExpectedNonce string
	PKCEVerifier  string
	CreatedAt     time.Time
}

// StateStore persists the mapping from state token to StateData for the
// duration of an OAuth2 authorization flow.
//
// Implementations MUST:
//
//   - be safe for concurrent use by multiple goroutines;
//   - treat Get as single-use: a successful Get returns the data AND
//     removes the entry atomically, so a replayed callback fails closed
//     with ErrStateNotFound;
//   - drop entries once their TTL has elapsed; ttl is the maximum, not
//     a hint — an implementation MAY purge earlier if necessary
//     (memory pressure, etc.), but MUST NOT extend it.
//
// The interface is small on purpose: a production implementation in
// Redis is a thin wrapper over SET key value EX ttl + GETDEL, and an
// in-memory implementation is a map plus a TTL goroutine — both of
// which fit naturally onto this surface. See MemoryStateStore for the
// reference in-process implementation.
type StateStore interface {
	// Put records data under state for at most ttl. Returns
	// ErrEmptyState if state is the empty string; other errors are
	// implementation-specific (e.g., Redis network failures).
	Put(ctx context.Context, state string, data StateData, ttl time.Duration) error

	// Get returns the data for state and atomically removes it from
	// the store. Returns ErrStateNotFound if state is unknown, has
	// expired, or has already been consumed.
	//
	// Implementations MUST guarantee atomicity: two concurrent Gets
	// for the same state must NOT both succeed.
	Get(ctx context.Context, state string) (StateData, error)
}
