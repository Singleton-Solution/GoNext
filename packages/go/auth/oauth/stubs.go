package oauth

// This file documents the concrete OAuth/OIDC providers that are
// reserved but not yet implemented. They live here so the design is
// visible to anyone reading the package without having to chase down
// the auth-permissions doc, and so the Registry has clear precedent
// for the IDs the built-in providers will claim ("google", "github").
//
// When these land, each becomes its own file (google.go, github.go)
// with a concrete type implementing Provider. Tests should follow the
// table-driven shape used by generic_oidc_test.go, with the IdP mocked
// via httptest.Server.
//
// # GoogleProvider (id="google")
//
// Authenticates against Google's OAuth2/OIDC endpoints. Google ships a
// well-formed discovery document at https://accounts.google.com/.well-known/openid-configuration,
// so GoogleProvider will be a thin wrapper over GenericOIDCProvider
// with two additions:
//
//   - hosted-domain (hd) restriction: operators that only want users
//     from a specific Google Workspace domain (e.g., "acme.com") pass
//     it as a config field, and the provider appends &hd=acme.com to
//     the authorization URL and checks the claim on the ID token.
//
//   - Display defaults: ID() returns "google", Name() returns "Google".
//
// The Registry already accepts "google" as a provider ID.
//
// # GitHubProvider (id="github")
//
// Authenticates against GitHub's classic OAuth2 endpoints. GitHub does
// NOT publish an OIDC discovery doc and does NOT issue ID tokens, so
// GitHubProvider implements Provider directly — it does not use
// go-oidc.
//
// Key differences from generic OIDC:
//
//   - UserInfo combines two REST calls: GET /user gives the public
//     profile, GET /user/emails gives the verified email list (which
//     can be private and absent from /user). The provider picks the
//     primary verified email.
//
//   - Sub is the GitHub user ID (an integer formatted as a string),
//     NOT the login handle — handles are mutable, IDs are not.
//
//   - ExpiresAt is left zero because GitHub OAuth2 tokens have no
//     stated expiry (they are revoked manually or via app
//     uninstallation).
//
// The Registry already accepts "github" as a provider ID.
//
// # Future: SAML / enterprise SSO
//
// SAML is explicitly out of scope for v1 (see docs/06-auth-permissions.md
// §1 Non-Goals). When it lands in v2 it will implement the same Provider
// interface, with AuthURL producing an SP-initiated SAML request and
// Exchange consuming the IdP's POST-binding response. The State store
// is reusable as-is — SAML's RelayState parameter slots into the same
// CSRF role state plays for OAuth2.
