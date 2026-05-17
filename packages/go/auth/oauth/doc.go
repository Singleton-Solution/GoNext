// Package oauth defines the OAuth2 / OpenID Connect provider contract for
// GoNext authentication.
//
// This package ships only the interface, the registry, the state store, and
// a single example provider — genericOIDC — that uses
// github.com/coreos/go-oidc/v3 to talk to any OIDC discovery-enabled IdP
// (i.e., any provider that publishes /.well-known/openid-configuration).
//
// Concrete first-party providers (Google, GitHub) come in follow-up issues.
// They will live as their own files in this package (google.go, github.go)
// and implement the same Provider interface. The deliberate separation lets
// this PR ship the contract and the registry without dragging in a long
// tail of provider-specific quirks (GitHub's /user/emails endpoint, Google's
// hosted-domain restriction, etc.).
//
// # Provider model
//
// A Provider is anything that can carry out the OAuth2 authorization-code
// flow plus a "tell me who this token belongs to" step. It is intentionally
// small — see the Provider interface — so a plugin can register a new
// provider by implementing five methods and calling Register.
//
// Five methods:
//
//   - ID()        — short stable identifier ("google", "github", "okta")
//   - Name()      — human display name for the login button
//   - AuthURL()   — builds the authorization URL the browser is redirected to
//   - Exchange()  — swaps an authorization code for tokens (access/ID/refresh)
//   - UserInfo()  — fetches the user identity (sub, email, name) from the IdP
//
// The Token and UserInfo types are deliberately wire-shape-agnostic. A
// provider may populate the ID token only (pure OIDC), the access token
// only (classic OAuth2 like GitHub), or both (Google). Likewise UserInfo
// can be sourced from the ID-token claims, from a separate /userinfo
// endpoint, or from a provider-specific REST call (/user, /user/emails).
//
// # State, nonce, and PKCE
//
// CSRF protection on the authorization-code flow requires a state parameter
// that survives the round-trip through the IdP. This package provides:
//
//   - State()                — generates a cryptographically random state
//     value (≥256 bits of entropy, base64url-encoded, URL-safe).
//   - StateStore             — the interface that persists the mapping from
//     state → {redirectURI, expectedNonce, createdAt}.
//   - MemoryStateStore       — an in-process map implementation suitable for
//     single-binary deployments and tests. Production deployments wire a
//     Redis-backed store (in follow-up issue) so a stateful redirect
//     survives both binary restarts and horizontal scale-out.
//
// State entries are single-use: StateStore.Get returns the data and removes
// the entry in one step. Replayed callbacks therefore fail closed.
//
// Nonce (the OIDC equivalent of state, but bound to the ID token) is also
// stored in StateData. Providers that issue ID tokens are expected to
// include this nonce in the authorization request and verify it at
// Exchange time. The generic OIDC provider implementation does so; the
// underlying go-oidc IDTokenVerifier checks the nonce claim against the
// stored value.
//
// PKCE is a per-provider concern, not a state-store concern: code_verifier
// is generated and stored alongside the state for providers that opt into
// PKCE. The genericOIDC provider supports PKCE; see its godoc.
//
// # Concrete providers (deferred)
//
// The following providers are reserved and will land in follow-up issues:
//
//   - GoogleProvider — uses Google's OIDC discovery doc + hosted-domain
//     (hd) restriction option. The Registry already supports id="google".
//   - GitHubProvider — uses GitHub's classic OAuth2 endpoints (no OIDC
//     discovery; the /user and /user/emails endpoints are scraped). The
//     UserInfo step combines both responses because GitHub does not put
//     the user's email in the primary /user payload when it's marked
//     private. The Registry already supports id="github".
//
// See docs/06-auth-permissions.md §4.4 and §4.7 for the full sequence
// diagram and §1 for the goals.
//
// # Redaction
//
// Token values (access, refresh, ID) MUST NOT appear in error messages or
// log lines. Providers that wrap upstream errors are expected to redact
// the body before surfacing it. This package follows the same discipline
// as packages/go/secrets: error strings describe the failure shape, never
// the secret. See docs/13-security-baseline.md §5.6.
package oauth
