package pat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// fixedCaps returns a UserCapsFunc that always reports the same caps
// for any user. Tests can override per case by capturing a different
// set in the closure.
func fixedCaps(caps policy.CapabilitySet) UserCapsFunc {
	return func(_ context.Context, _ string) (policy.CapabilitySet, error) {
		return caps, nil
	}
}

// inner is a sentinel handler used to confirm whether Middleware
// reached the wrapped handler. It also surfaces the principal and the
// intersected caps to the response so the test can assert them.
type innerProbe struct {
	called    bool
	principal policy.Principal
	caps      policy.CapabilitySet
	tokenID   string
}

func (p *innerProbe) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.called = true
		p.principal, _ = policy.FromContext(r.Context())
		p.caps, _ = CapsFromContext(r.Context())
		p.tokenID, _ = TokenIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

// issueInto is a helper that mints a fresh PAT into the given store
// and returns the plaintext.
func issueInto(t *testing.T, s Store, userID string, scopes []string, expiresAt *time.Time) string {
	t.Helper()
	plaintext, row, hash, err := New(userID, "test", scopes, expiresAt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Issue(context.Background(), row, hash); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return plaintext
}

// TestMiddleware_ValidToken — happy path: token is recognised, the
// Principal lands on the context with UserID set, and the intersected
// caps are the narrower of (scopes, user-caps).
func TestMiddleware_ValidToken(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	plaintext := issueInto(t, s, "user:1", []string{"read", "edit_posts"}, nil)

	userCaps := policy.NewCapabilitySet(policy.CapRead, policy.CapEditPosts, policy.CapManageOptions)
	probe := &innerProbe{}
	mw := Middleware(Config{Store: s, UserCaps: fixedCaps(userCaps)})
	srv := httptest.NewServer(mw(probe.handler()))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", res.StatusCode)
	}
	if !probe.called {
		t.Fatal("inner handler not reached")
	}
	if probe.principal.UserID != "user:1" {
		t.Fatalf("principal UserID: %q", probe.principal.UserID)
	}
	if !probe.caps.Has(policy.CapRead) || !probe.caps.Has(policy.CapEditPosts) {
		t.Fatalf("expected intersected caps to include read+edit_posts: %v", probe.caps.All())
	}
	// Token did NOT request manage_options even though user has it →
	// must be absent from the intersected set.
	if probe.caps.Has(policy.CapManageOptions) {
		t.Fatal("manage_options leaked into intersected caps")
	}
}

// TestMiddleware_NoHeader_FallsThrough — when no Authorization header
// is present, the middleware is transparent.
func TestMiddleware_NoHeader_FallsThrough(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	probe := &innerProbe{}
	mw := Middleware(Config{Store: s, UserCaps: fixedCaps(nil)})
	srv := httptest.NewServer(mw(probe.handler()))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !probe.called {
		t.Fatalf("expected fall-through to 200, got status=%d called=%v",
			res.StatusCode, probe.called)
	}
	if probe.principal.UserID != "" {
		t.Fatalf("no header should not set principal: %+v", probe.principal)
	}
}

// TestMiddleware_InvalidToken_401 — well-shaped but unknown token.
func TestMiddleware_InvalidToken_401(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	probe := &innerProbe{}
	mw := Middleware(Config{Store: s, UserCaps: fixedCaps(nil)})
	srv := httptest.NewServer(mw(probe.handler()))
	defer srv.Close()

	// Generate a well-shaped token but never issue it.
	plaintext, _, _, err := New("user:1", "x", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: %d want 401", res.StatusCode)
	}
	if probe.called {
		t.Fatal("inner handler must NOT be reached")
	}
}

// TestMiddleware_ExpiredToken_401 — token row exists but expires_at
// is in the past.
func TestMiddleware_ExpiredToken_401(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	past := time.Now().Add(-1 * time.Hour).UTC()
	plaintext := issueInto(t, s, "user:1", []string{"read"}, &past)

	probe := &innerProbe{}
	mw := Middleware(Config{Store: s, UserCaps: fixedCaps(nil)})
	srv := httptest.NewServer(mw(probe.handler()))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: %d want 401", res.StatusCode)
	}
}

// TestMiddleware_RevokedToken_401 — once revoked, the bearer is 401.
func TestMiddleware_RevokedToken_401(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	plaintext, row, hash, err := New("user:1", "x", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := s.Issue(context.Background(), row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Revoke(context.Background(), "user:1", stored.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	probe := &innerProbe{}
	mw := Middleware(Config{Store: s, UserCaps: fixedCaps(nil)})
	srv := httptest.NewServer(mw(probe.handler()))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: %d want 401", res.StatusCode)
	}
}

// TestIntersect — token with a wider scope than the user has gets the
// user's narrower set; token with no scopes is empty.
func TestIntersect(t *testing.T) {
	t.Parallel()
	userCaps := policy.NewCapabilitySet(policy.CapRead, policy.CapEditPosts)
	cases := []struct {
		name   string
		scopes []string
		want   []policy.Capability
	}{
		{
			name:   "subset",
			scopes: []string{"read"},
			want:   []policy.Capability{policy.CapRead},
		},
		{
			name:   "tries-to-escalate",
			scopes: []string{"manage_options"},
			want:   nil,
		},
		{
			name:   "intersection",
			scopes: []string{"read", "manage_options", "edit_posts"},
			want:   []policy.Capability{policy.CapRead, policy.CapEditPosts},
		},
		{
			name:   "empty-scopes",
			scopes: nil,
			want:   nil,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := Intersect(c.scopes, userCaps)
			if got, want := len(got), len(c.want); got != want {
				t.Fatalf("len: %d want %d", got, want)
			}
			for _, w := range c.want {
				if !got.Has(w) {
					t.Fatalf("missing %s in %v", w, got.All())
				}
			}
		})
	}
}

// TestRequire_PATScopeIsAuthoritative — a token scoped to {read}
// satisfies CapRead but NOT CapEditPosts, regardless of user caps.
func TestRequire_PATScopeIsAuthoritative(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	// Token scoped to read only, but user has both.
	plaintext := issueInto(t, s, "user:1", []string{"read"}, nil)
	userCaps := policy.NewCapabilitySet(policy.CapRead, policy.CapEditPosts)

	probeOK := &innerProbe{}
	probeForbidden := &innerProbe{}

	// Compose: Middleware → Require(CapRead) → probeOK.
	mwAuth := Middleware(Config{Store: s, UserCaps: fixedCaps(userCaps)})
	pol := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	srvOK := httptest.NewServer(mwAuth(Require(pol, policy.CapRead)(probeOK.handler())))
	srvForbidden := httptest.NewServer(mwAuth(Require(pol, policy.CapEditPosts)(probeForbidden.handler())))
	defer srvOK.Close()
	defer srvForbidden.Close()

	// CapRead — token grants it.
	{
		req, _ := http.NewRequest("GET", srvOK.URL, nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("read: status %d want 200", res.StatusCode)
		}
		if !probeOK.called {
			t.Fatal("read: inner not reached")
		}
	}
	// CapEditPosts — user has it but token doesn't scope it. Must 403.
	{
		req, _ := http.NewRequest("GET", srvForbidden.URL, nil)
		req.Header.Set("Authorization", "Bearer "+plaintext)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("edit_posts: status %d want 403", res.StatusCode)
		}
		if probeForbidden.called {
			t.Fatal("edit_posts: inner must NOT be reached")
		}
	}
}
