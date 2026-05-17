package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// GenericOIDCConfig configures a GenericOIDCProvider against any OpenID
// Connect identity provider that publishes a discovery document at
// /.well-known/openid-configuration.
//
// IssuerURL is the canonical URL the IdP publishes (e.g.,
// "https://accounts.google.com" or "https://your-tenant.okta.com"). The
// provider library appends the discovery path itself.
//
// ClientID and ClientSecret are the OAuth2 client credentials the
// operator registered with the IdP. ClientSecret must come from the
// process-wide secrets.Store (env, file mount, KMS), NEVER from the
// database in plaintext — see docs/13-security-baseline.md §5.
//
// Scopes are appended to the implied "openid" scope. Most installs want
// at least "email" and "profile" so UserInfo can produce a usable
// identity record. Empty Scopes means openid-only, which is sufficient
// for "is this human authorized" but not for "what's their email".
//
// ID and Name override the defaults ("generic-oidc" / "Generic OIDC").
// Operators configuring multiple OIDC providers (a corporate Keycloak
// plus a customer-facing Auth0) MUST set distinct IDs.
//
// HTTPClient lets the caller inject a custom http.Client — useful in
// tests, and in production for installs that need to set custom TLS
// roots or route through a proxy. nil means http.DefaultClient.
type GenericOIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	Scopes       []string
	ID           string
	Name         string
	HTTPClient   *http.Client
}

// GenericOIDCProvider is a Provider that speaks pure OIDC against any
// discovery-enabled IdP. It is the reference implementation in this
// package; first-party providers (Google, GitHub) ship in follow-up
// issues with their own files.
//
// Construction validates the discovery doc up front via NewGenericOIDC,
// so a broken issuer URL fails at boot rather than at first login. The
// resulting *GenericOIDCProvider is safe for concurrent use.
type GenericOIDCProvider struct {
	id       string
	name     string
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   *oauth2.Config
	scopes   []string
	hc       *http.Client
}

// NewGenericOIDC constructs a GenericOIDCProvider by fetching the IdP's
// discovery document.
//
// Errors:
//
//   - if IssuerURL or ClientID is empty (programmer bug, fail loud);
//   - if the discovery fetch fails (IdP unreachable, TLS mismatch,
//     bad issuer URL).
//
// The returned provider has the openid scope implicitly; cfg.Scopes
// extends it.
func NewGenericOIDC(ctx context.Context, cfg GenericOIDCConfig) (*GenericOIDCProvider, error) {
	if cfg.IssuerURL == "" {
		return nil, fmt.Errorf("%w: IssuerURL is empty", ErrInvalidProviderID)
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("%w: ClientID is empty", ErrInvalidProviderID)
	}

	id := cfg.ID
	if id == "" {
		id = "generic-oidc"
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	name := cfg.Name
	if name == "" {
		name = "Generic OIDC"
	}

	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}

	// go-oidc reads the HTTP client off the context for the discovery
	// fetch. We rebuild the context here so that an HTTPClient supplied
	// via config is honoured without the caller having to know about
	// go-oidc's internal convention.
	discoverCtx := oidc.ClientContext(ctx, hc)

	provider, err := oidc.NewProvider(discoverCtx, cfg.IssuerURL)
	if err != nil {
		// The error from go-oidc is already redacted (it's an HTTP body
		// or a JSON parse error against a well-known shape, neither of
		// which carries the client secret). Wrap with a clear shape
		// reason but keep it tight.
		return nil, fmt.Errorf("oauth: oidc discovery for %q: %w", cfg.IssuerURL, err)
	}

	scopes := append([]string{oidc.ScopeOpenID}, cfg.Scopes...)

	oc := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
		// RedirectURL is set per-request in AuthURL / Exchange so a
		// single provider can serve multiple frontend hostnames
		// (admin vs site) without re-instantiation.
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	return &GenericOIDCProvider{
		id:       id,
		name:     name,
		provider: provider,
		verifier: verifier,
		oauth2:   oc,
		scopes:   scopes,
		hc:       hc,
	}, nil
}

// ID returns the provider identifier; "generic-oidc" by default.
func (p *GenericOIDCProvider) ID() string { return p.id }

// Name returns the human display name; "Generic OIDC" by default.
func (p *GenericOIDCProvider) Name() string { return p.name }

// AuthURL builds the authorization URL the browser is redirected to.
//
// The state parameter is passed through verbatim — the caller is
// responsible for persisting it via StateStore.Put before redirecting.
// redirectURI must exactly match the URI the IdP will eventually call
// back, and must be one of the URIs registered with the IdP.
//
// AuthURL does not set the nonce parameter. Callers that want OIDC
// nonce verification should append it themselves with the
// oidc.Nonce("...") helper — or, in this package, generate one via
// Nonce(), store it in StateData.ExpectedNonce, and pass it through the
// authorization URL via a wrapper. Keeping AuthURL minimal here means
// non-OIDC providers (a future GitHub OAuth2 impl) can share the same
// signature.
func (p *GenericOIDCProvider) AuthURL(state, redirectURI string) string {
	// Clone the oauth2 config so concurrent flows with different
	// redirect URIs do not race on the shared field.
	cfg := *p.oauth2
	cfg.RedirectURL = redirectURI
	return cfg.AuthCodeURL(state)
}

// Exchange swaps an authorization code for a Token. The ID token is
// verified against the IdP's JWKS as part of the exchange; if
// verification fails the error is returned and no Token is produced.
//
// The redirectURI must exactly match the value previously passed to
// AuthURL — most IdPs check this and will reject the exchange otherwise.
func (p *GenericOIDCProvider) Exchange(ctx context.Context, code, redirectURI string) (*Token, error) {
	if code == "" {
		return nil, fmt.Errorf("oauth: %s: exchange called with empty code", p.id)
	}
	cfg := *p.oauth2
	cfg.RedirectURL = redirectURI

	// Inject our HTTP client into the ctx so go-oidc and oauth2 use it.
	excCtx := oidc.ClientContext(ctx, p.hc)
	tok, err := cfg.Exchange(excCtx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth: %s: code exchange: %w", p.id, redactExchangeErr(err))
	}

	out := &Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.Expiry,
	}

	// Pull the id_token out of the extras and verify it. Some OIDC
	// providers (and some legacy non-OIDC clients) skip id_token; we
	// don't require it here, but we do verify it when present.
	if raw, ok := tok.Extra("id_token").(string); ok && raw != "" {
		if _, vErr := p.verifier.Verify(excCtx, raw); vErr != nil {
			return nil, fmt.Errorf("oauth: %s: id_token verify: %w", p.id, vErr)
		}
		out.IDToken = raw
	}

	return out, nil
}

// UserInfo fetches the identity record associated with tok by calling the
// IdP's /userinfo endpoint (whatever URL the discovery doc named).
//
// If tok has no AccessToken, UserInfo returns an error: /userinfo
// requires the access token. Callers that have only an ID token should
// parse the claims directly from Token.IDToken using go-oidc's
// IDTokenVerifier.Verify — kept out of this method to preserve the
// "one source of truth" property of UserInfo.
func (p *GenericOIDCProvider) UserInfo(ctx context.Context, tok *Token) (*UserInfo, error) {
	if tok == nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("oauth: %s: userinfo called without an access token", p.id)
	}

	uCtx := oidc.ClientContext(ctx, p.hc)
	src := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: tok.AccessToken,
		Expiry:      tok.ExpiresAt,
	})
	ui, err := p.provider.UserInfo(uCtx, src)
	if err != nil {
		return nil, fmt.Errorf("oauth: %s: userinfo fetch: %w", p.id, err)
	}

	var claims struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		Locale        string `json:"locale"`
	}
	if err := ui.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oauth: %s: userinfo claims: %w", p.id, err)
	}

	// /userinfo's "sub" is the canonical subject — but the go-oidc
	// UserInfo struct exposes its own Subject field too. Prefer the
	// struct field when the claims one is empty so providers that put
	// the sub only in a header still work.
	sub := claims.Sub
	if sub == "" {
		sub = ui.Subject
	}
	if sub == "" {
		return nil, fmt.Errorf("oauth: %s: userinfo missing sub", p.id)
	}

	return &UserInfo{
		Sub:           sub,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
		Locale:        claims.Locale,
	}, nil
}

// redactExchangeErr scrubs the token exchange error so that an upstream
// response containing the client secret or an access token (some IdPs
// include them in debug fields of error bodies) is never surfaced
// verbatim.
//
// The heuristic here is intentionally conservative: oauth2.RetrieveError
// has a Body field that's the raw response — we drop it and keep only
// the HTTP status. A real production implementation can be more
// surgical (parse the JSON, keep error/error_description fields), but
// the safe default is "name the failure, drop the body".
func redactExchangeErr(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		// Status text is harmless; ErrorCode and ErrorDescription
		// follow RFC 6749 §5.2 — safe to surface, never contain
		// secrets.
		parts := []string{}
		if re.Response != nil {
			parts = append(parts, fmt.Sprintf("status=%d", re.Response.StatusCode))
		}
		if re.ErrorCode != "" {
			parts = append(parts, fmt.Sprintf("code=%q", re.ErrorCode))
		}
		if re.ErrorDescription != "" {
			parts = append(parts, fmt.Sprintf("desc=%q", re.ErrorDescription))
		}
		if len(parts) == 0 {
			return errors.New("idp rejected exchange")
		}
		return errors.New("idp rejected exchange: " + strings.Join(parts, " "))
	}
	return err
}

