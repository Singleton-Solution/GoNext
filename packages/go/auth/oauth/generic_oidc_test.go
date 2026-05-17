package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockOIDCServer is a tiny in-process IdP that publishes the four
// endpoints go-oidc needs: discovery, JWKS, token, and userinfo.
//
// The signing key is regenerated per test, so the JWKS endpoint reflects
// what was used to mint the ID token.
type mockOIDCServer struct {
	t        *testing.T
	server   *httptest.Server
	signer   jose.Signer
	pubKey   *rsa.PublicKey
	kid      string
	clientID string

	// Knobs the test can tweak between requests:
	authCode      string
	accessToken   string
	idTokenClaims map[string]any
	userInfo      map[string]any
	expiresIn     int

	// Failure mode toggles
	tokenFailStatus int    // if non-zero, /token returns this status
	tokenFailBody   string // body to return with tokenFailStatus
}

func newMockOIDCServer(t *testing.T, clientID string) *mockOIDCServer {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}

	kid := "test-key-1"
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid),
	)
	if err != nil {
		t.Fatalf("jose signer: %v", err)
	}

	m := &mockOIDCServer{
		t:           t,
		signer:      signer,
		pubKey:      &priv.PublicKey,
		kid:         kid,
		clientID:    clientID,
		authCode:    "the-code",
		accessToken: "the-access-token",
		expiresIn:   3600,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", m.handleDiscovery)
	mux.HandleFunc("/jwks", m.handleJWKS)
	mux.HandleFunc("/token", m.handleToken)
	mux.HandleFunc("/userinfo", m.handleUserInfo)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// We never actually drive the browser flow in tests; this is
		// just so a discovery client that probes the URL doesn't 404.
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.server = srv
	return m
}

func (m *mockOIDCServer) URL() string { return m.server.URL }

func (m *mockOIDCServer) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	doc := map[string]any{
		"issuer":                 m.URL(),
		"authorization_endpoint": m.URL() + "/authorize",
		"token_endpoint":         m.URL() + "/token",
		"userinfo_endpoint":      m.URL() + "/userinfo",
		"jwks_uri":               m.URL() + "/jwks",
		"response_types_supported": []string{
			"code", "id_token", "token id_token",
		},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (m *mockOIDCServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	jwk := map[string]any{
		"keys": []any{
			map[string]any{
				"kty": "RSA",
				"kid": m.kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(m.pubKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(m.pubKey.E)).Bytes()),
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwk)
}

func (m *mockOIDCServer) signIDToken(claims map[string]any) string {
	m.t.Helper()
	// Default claims; merge with caller-supplied.
	defaults := map[string]any{
		"iss": m.URL(),
		"aud": m.clientID,
		"sub": "user-1",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range claims {
		defaults[k] = v
	}
	out, err := jwt.Signed(m.signer).Claims(defaults).Serialize()
	if err != nil {
		m.t.Fatalf("sign id token: %v", err)
	}
	return out
}

func (m *mockOIDCServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if m.tokenFailStatus != 0 {
		w.WriteHeader(m.tokenFailStatus)
		_, _ = w.Write([]byte(m.tokenFailBody))
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("grant_type") != "authorization_code" {
		http.Error(w, "bad grant_type", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("code") != m.authCode {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"unknown code"}`))
		return
	}
	idToken := m.signIDToken(m.idTokenClaims)
	resp := map[string]any{
		"access_token": m.accessToken,
		"token_type":   "Bearer",
		"expires_in":   m.expiresIn,
		"id_token":     idToken,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *mockOIDCServer) handleUserInfo(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "no bearer", http.StatusUnauthorized)
		return
	}
	if strings.TrimPrefix(auth, "Bearer ") != m.accessToken {
		http.Error(w, "wrong token", http.StatusUnauthorized)
		return
	}
	body := m.userInfo
	if body == nil {
		body = map[string]any{
			"sub":            "user-1",
			"email":          "alice@example.com",
			"email_verified": true,
			"name":           "Alice Example",
			"picture":        "https://idp.example/avatar/alice",
			"locale":         "en-US",
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func TestGenericOIDC_AuthURL(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL:    m.URL(),
		ClientID:     "client-abc",
		ClientSecret: "secret-xyz",
		Scopes:       []string{"email", "profile"},
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}

	u := p.AuthURL("state-123", "https://app/callback")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("AuthURL not a URL: %v", err)
	}
	q := parsed.Query()
	if got := q.Get("state"); got != "state-123" {
		t.Errorf("state = %q, want state-123", got)
	}
	if got := q.Get("client_id"); got != "client-abc" {
		t.Errorf("client_id = %q, want client-abc", got)
	}
	if got := q.Get("redirect_uri"); got != "https://app/callback" {
		t.Errorf("redirect_uri = %q, want https://app/callback", got)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q, want code", got)
	}
	scope := q.Get("scope")
	for _, want := range []string{"openid", "email", "profile"} {
		if !strings.Contains(scope, want) {
			t.Errorf("scope %q missing %q", scope, want)
		}
	}
	// Client secret must NEVER appear in the URL.
	if strings.Contains(u, "secret-xyz") {
		t.Errorf("AuthURL leaked client secret: %q", u)
	}
}

func TestGenericOIDC_ExchangeAndUserInfo(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL:    m.URL(),
		ClientID:     "client-abc",
		ClientSecret: "secret-xyz",
		Scopes:       []string{"email", "profile"},
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}

	// Customize the ID token claims so we can verify they propagate.
	m.idTokenClaims = map[string]any{
		"sub":   "user-42",
		"email": "alice@example.com",
	}

	ctx := context.Background()
	tok, err := p.Exchange(ctx, "the-code", "https://app/callback")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "the-access-token" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "the-access-token")
	}
	if tok.IDToken == "" {
		t.Error("IDToken is empty")
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero")
	}

	ui, err := p.UserInfo(ctx, tok)
	if err != nil {
		t.Fatalf("UserInfo: %v", err)
	}
	if ui.Sub != "user-1" {
		t.Errorf("Sub = %q, want user-1 (from /userinfo)", ui.Sub)
	}
	if ui.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", ui.Email)
	}
	if !ui.EmailVerified {
		t.Errorf("EmailVerified = false, want true")
	}
	if ui.Name != "Alice Example" {
		t.Errorf("Name = %q, want Alice Example", ui.Name)
	}
	if ui.Picture == "" {
		t.Errorf("Picture is empty")
	}
	if ui.Locale != "en-US" {
		t.Errorf("Locale = %q, want en-US", ui.Locale)
	}
}

func TestGenericOIDC_ExchangeUnknownCode(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL:    m.URL(),
		ClientID:     "client-abc",
		ClientSecret: "secret-xyz",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}

	_, err = p.Exchange(context.Background(), "not-the-code", "https://app/callback")
	if err == nil {
		t.Fatal("Exchange with bad code: err = nil, want non-nil")
	}
	// Failure must NOT contain the client secret.
	if strings.Contains(err.Error(), "secret-xyz") {
		t.Errorf("Exchange error leaked client secret: %v", err)
	}
}

func TestGenericOIDC_ExchangeEmptyCode(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(), ClientID: "client-abc",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}
	_, err = p.Exchange(context.Background(), "", "https://app/callback")
	if err == nil {
		t.Fatal("Exchange with empty code: err = nil, want non-nil")
	}
}

func TestGenericOIDC_UserInfoNoToken(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(), ClientID: "client-abc",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}

	_, err = p.UserInfo(context.Background(), nil)
	if err == nil {
		t.Error("UserInfo(nil): err = nil, want non-nil")
	}
	_, err = p.UserInfo(context.Background(), &Token{})
	if err == nil {
		t.Error("UserInfo(empty token): err = nil, want non-nil")
	}
}

func TestGenericOIDC_UserInfoMissingSub(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	m.userInfo = map[string]any{
		// Deliberately omit "sub".
		"email": "alice@example.com",
	}
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(), ClientID: "client-abc",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}

	ctx := context.Background()
	tok, err := p.Exchange(ctx, "the-code", "https://app/callback")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	// Note: go-oidc itself rejects userinfo with missing sub at decode
	// time. Either return-path is acceptable — we just must not return
	// a usable UserInfo. Accept any non-nil error.
	_, err = p.UserInfo(ctx, tok)
	if err == nil {
		t.Error("UserInfo with missing sub: err = nil, want non-nil")
	}
}

func TestGenericOIDC_BadIssuerURL(t *testing.T) {
	// Pointing at a server that 404s on discovery should fail at
	// construction, not at first Exchange.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: srv.URL,
		ClientID:  "client-abc",
	})
	if err == nil {
		t.Fatal("NewGenericOIDC with bad issuer: err = nil, want non-nil")
	}
}

func TestGenericOIDC_EmptyIssuerOrClient(t *testing.T) {
	cases := []GenericOIDCConfig{
		{IssuerURL: "", ClientID: "x"},
		{IssuerURL: "https://x", ClientID: ""},
	}
	for i, c := range cases {
		_, err := NewGenericOIDC(context.Background(), c)
		if !errors.Is(err, ErrInvalidProviderID) {
			t.Errorf("case %d (cfg=%+v): err = %v, want errors.Is ErrInvalidProviderID", i, c, err)
		}
	}
}

func TestGenericOIDC_CustomID(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(),
		ClientID:  "client-abc",
		ID:        "okta-corp",
		Name:      "Corporate Okta",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}
	if p.ID() != "okta-corp" {
		t.Errorf("ID = %q, want okta-corp", p.ID())
	}
	if p.Name() != "Corporate Okta" {
		t.Errorf("Name = %q, want Corporate Okta", p.Name())
	}
}

func TestGenericOIDC_DefaultIDAndName(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(), ClientID: "client-abc",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}
	if p.ID() != "generic-oidc" {
		t.Errorf("ID = %q, want generic-oidc", p.ID())
	}
	if p.Name() != "Generic OIDC" {
		t.Errorf("Name = %q, want 'Generic OIDC'", p.Name())
	}
}

func TestGenericOIDC_BadProviderID(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	_, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(), ClientID: "client-abc",
		ID: "Bad ID",
	})
	if !errors.Is(err, ErrInvalidProviderID) {
		t.Errorf("NewGenericOIDC with bad ID: err = %v, want errors.Is ErrInvalidProviderID", err)
	}
}

func TestGenericOIDC_ExchangeIDTokenWrongAudienceRejected(t *testing.T) {
	m := newMockOIDCServer(t, "client-abc")
	p, err := NewGenericOIDC(context.Background(), GenericOIDCConfig{
		IssuerURL: m.URL(),
		ClientID:  "client-abc",
	})
	if err != nil {
		t.Fatalf("NewGenericOIDC: %v", err)
	}

	// Mint an ID token for a different client ID. Verification must
	// reject it.
	m.idTokenClaims = map[string]any{
		"aud": "another-client",
	}
	_, err = p.Exchange(context.Background(), "the-code", "https://app/callback")
	if err == nil {
		t.Fatal("Exchange with wrong audience: err = nil, want non-nil")
	}
}

// Compile-time assertion: GenericOIDCProvider implements Provider.
var _ Provider = (*GenericOIDCProvider)(nil)

// Used by tests to confirm the mock IdP composition works.
func TestMockOIDCServer_DiscoveryReachable(t *testing.T) {
	m := newMockOIDCServer(t, "x")
	resp, err := http.Get(m.URL() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("discovery GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("discovery status = %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("discovery decode: %v", err)
	}
	if got := doc["issuer"]; got != m.URL() {
		t.Errorf("discovery issuer = %v, want %s", got, m.URL())
	}
}

