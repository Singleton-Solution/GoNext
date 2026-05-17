package oauth

import (
	"context"
	"errors"
	"time"
)

// Errors returned by this package. Callers should compare with errors.Is
// rather than string-matching; the wrapped detail may grow more verbose
// over time but the sentinel set is stable.
var (
	// ErrProviderNotFound is returned by Registry.Get when no provider is
	// registered under the requested ID. It is a normal control-flow
	// error — callers presenting a login menu will hit this for any IdP
	// the operator has not configured.
	ErrProviderNotFound = errors.New("oauth: provider not found")

	// ErrDuplicateProvider is returned by Registry.Register when a
	// provider with the same ID is already present. Re-registration is
	// always a programming error; in particular it would silently shadow
	// a previously-registered provider, which would be a security bug
	// for a plugin-loaded provider trying to override a built-in.
	ErrDuplicateProvider = errors.New("oauth: provider already registered")

	// ErrInvalidProviderID is returned by Registry.Register when the
	// provider's ID is empty or otherwise rejected by the registry. IDs
	// are stored in the user_external_identities table as a TEXT column
	// (see docs/06-auth-permissions.md §2.2), so they must be stable.
	ErrInvalidProviderID = errors.New("oauth: invalid provider id")

	// ErrStateNotFound is returned by StateStore.Get when the state
	// parameter is unknown — either it was never issued, or it has
	// already been consumed (state entries are single-use), or its TTL
	// has elapsed. Callers should treat all three the same way: 400
	// Bad Request, with the user redirected back to the login form.
	ErrStateNotFound = errors.New("oauth: state not found or expired")

	// ErrEmptyState is returned by StateStore.Put when the state key is
	// empty. State values must be ≥256 bits of entropy; an empty string
	// would cause two concurrent flows to clobber each other.
	ErrEmptyState = errors.New("oauth: state must not be empty")
)

// Token holds the artefacts a provider returns when an authorization code
// is exchanged. The shape is provider-agnostic: pure-OIDC providers leave
// AccessToken empty and populate IDToken; classic OAuth2 providers do the
// reverse; many populate all three.
//
// ExpiresAt is the wall-clock instant at which AccessToken should be
// considered expired. Providers that return only a relative expires_in
// must add it to time.Now() at Exchange time. A zero ExpiresAt means the
// provider did not return an expiry — callers should treat the token as
// short-lived and not cache it across requests.
//
// Token values are secrets. They MUST NOT appear in error messages, log
// lines, or audit-log payloads. The struct does not implement a String or
// MarshalJSON method on purpose: anyone serialising a Token is opting in
// explicitly.
type Token struct {
	// AccessToken is the bearer token used to call the provider's APIs.
	// Empty for providers that do not issue access tokens (some OIDC-only
	// configurations).
	AccessToken string

	// RefreshToken, if non-empty, can be used to obtain a fresh access
	// token without user interaction. Issued only when the authorization
	// request asked for offline_access (OIDC) or when the provider's
	// scope set includes refresh capability (GitHub, Google).
	//
	// Refresh-token handling (rotation, persistence) is the caller's
	// responsibility — this package does not store them.
	RefreshToken string

	// IDToken is the OIDC ID token (a signed JWT). Empty for non-OIDC
	// providers (e.g., GitHub classic OAuth2). When present, the signing
	// chain has already been verified by the provider implementation;
	// callers may parse claims without re-verifying.
	IDToken string

	// ExpiresAt is the wall-clock expiry of AccessToken. Zero means the
	// provider did not say.
	ExpiresAt time.Time
}

// UserInfo is the normalized identity record a provider returns. Fields
// not asserted by the IdP are left at the zero value; in particular,
// EmailVerified defaults to false, which means "we don't know" — callers
// holding a verified-email constraint must check it explicitly.
//
// Sub is the IdP's stable user identifier. The pair (provider_id, Sub) is
// the foreign key into user_external_identities; see
// docs/06-auth-permissions.md §2.2.
//
// Picture and Locale are advisory hints: providers do not always supply
// them, and callers should fall back to in-product defaults if either is
// empty.
type UserInfo struct {
	// Sub is the provider's stable, opaque identifier for the user.
	// This field is REQUIRED — a provider returning a UserInfo without
	// a Sub has produced a meaningless identity and the caller should
	// reject the login.
	Sub string

	// Email is the user's email address as asserted by the IdP. May be
	// empty if the user has not consented to email scope.
	Email string

	// EmailVerified is true iff the IdP has stated the email is verified
	// (typically via the OIDC email_verified claim or a GitHub
	// /user/emails verified flag). Defaults to false; treat false as
	// "unknown" rather than "verified=false", because some providers
	// simply omit the claim.
	EmailVerified bool

	// Name is the human display name. Falls back to the empty string
	// when the IdP does not supply one; the caller is expected to use
	// the local part of the email as a default.
	Name string

	// Picture is the URL of the user's avatar at the IdP. Optional —
	// downstream UI is expected to fall back to a Gravatar lookup or to
	// an initials avatar.
	Picture string

	// Locale is the user's preferred locale as a BCP-47 tag (e.g.
	// "en-US"). Optional.
	Locale string
}

// Provider is the contract every OAuth2 / OIDC integration implements.
//
// Implementations MUST be safe for concurrent use: GoNext serves login
// flows concurrently and a single Provider instance is shared across all
// of them. State is held in the StateStore, not on the Provider.
//
// Implementations SHOULD wrap upstream errors with redacted context. In
// particular: an IdP's error body may include the raw access token in
// debug fields, and surfacing it would constitute a credential leak. The
// generic OIDC implementation in this package is the reference for how
// to do this safely.
type Provider interface {
	// ID returns the short, stable identifier for this provider. It is
	// what gets stored in the user_external_identities.provider column
	// and what callers pass to Registry.Get. Lowercase ASCII; "google",
	// "github", "okta", "generic-oidc".
	ID() string

	// Name returns the human-readable display name for the provider,
	// used as the label on the "Continue with …" button. Free-form
	// (mixed case, spaces, branding allowed).
	Name() string

	// AuthURL returns the URL the user's browser should be redirected
	// to in order to initiate the authorization flow.
	//
	// state is the opaque CSRF token previously issued by State() and
	// persisted via StateStore.Put. The caller is expected to recover
	// the state on callback and pass it to StateStore.Get to retrieve
	// the matching redirectURI and nonce.
	//
	// redirectURI is the URL the IdP will redirect the browser back to
	// after the user consents (or denies). It must exactly match one
	// of the redirect URIs registered with the IdP.
	AuthURL(state, redirectURI string) string

	// Exchange swaps an authorization code for a Token.
	//
	// code is the value the IdP placed on the callback URL as ?code=…
	// redirectURI MUST be exactly the same value that was passed to
	// AuthURL — many IdPs (Google, Okta) reject the exchange if the
	// two don't match, as a defense against replay across redirect
	// targets.
	//
	// On success, the returned Token has AccessToken or IDToken set
	// (or both), and ExpiresAt populated when the IdP returns expiry.
	//
	// On failure, the error is wrapped with the failure shape but
	// contains no token material, no client secret, and no full
	// IdP response body (which can itself contain credentials).
	Exchange(ctx context.Context, code, redirectURI string) (*Token, error)

	// UserInfo fetches the identity record associated with tok. The
	// provider may source this from the ID token's claims, from a
	// /userinfo endpoint, or from a provider-specific REST endpoint
	// (e.g., GitHub's /user). The contract is the same: Sub must be
	// non-empty on success.
	//
	// Implementations MUST NOT trust an IdP's email_verified claim
	// silently: it should be propagated to UserInfo.EmailVerified
	// faithfully, and the *caller* (see docs/06-auth-permissions.md
	// §4.4) is responsible for the auto-link safety rule.
	UserInfo(ctx context.Context, tok *Token) (*UserInfo, error)
}
