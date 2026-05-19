package tokens

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/pat"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// withPrincipal wraps a handler with a middleware that injects a fixed
// Principal. Production wires the session/PAT middleware here; tests
// just want a Principal on the context.
func withPrincipal(p policy.Principal, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(policy.WithPrincipal(r.Context(), p))
		h.ServeHTTP(w, r)
	})
}

// newServer wires Mount + a principal-injecting middleware. Returns
// the server URL and the store (so tests can pre-seed rows).
func newServer(t *testing.T, p policy.Principal, caps policy.CapabilitySet) (string, pat.Store, func()) {
	t.Helper()
	store := pat.NewMemoryStore()
	mux := http.NewServeMux()
	err := Mount(mux, "/api/v1/me/tokens", Deps{
		Store: store,
		UserCaps: func(_ context.Context, _ string) (policy.CapabilitySet, error) {
			return caps, nil
		},
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(withPrincipal(p, mux))
	return srv.URL, store, srv.Close
}

func decode(t *testing.T, r *http.Response, v any) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, body)
	}
}

// TestIssue_HappyPath — operator submits valid input; response carries
// the plaintext once, save_now=true, effective_scopes = scopes ∩ caps.
func TestIssue_HappyPath(t *testing.T) {
	t.Parallel()
	pr := policy.Principal{UserID: "user:1"}
	caps := policy.NewCapabilitySet(policy.CapRead, policy.CapEditPosts)
	url, store, cleanup := newServer(t, pr, caps)
	defer cleanup()

	body, _ := json.Marshal(IssueRequest{
		Name:      "ci-token",
		Scopes:    []string{"read", "edit_posts", "manage_options"},
		ExpiresIn: "30d",
	})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d body=%s", res.StatusCode, raw)
	}
	var view IssuedTokenView
	decode(t, res, &view)
	if view.Plaintext == "" || !strings.HasPrefix(view.Plaintext, "gnp_") {
		t.Fatalf("plaintext missing or wrong shape: %q", view.Plaintext)
	}
	if !view.SaveNow {
		t.Fatal("save_now must be true on the issue response")
	}
	// effective_scopes is the intersection — manage_options is dropped.
	if got := view.EffectiveScopes; !containsAll(got, []string{"read", "edit_posts"}) || contains(got, "manage_options") {
		t.Fatalf("effective_scopes wrong: %v", got)
	}
	// The row landed in the store.
	rows, err := store.List(context.Background(), "user:1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := len(rows), 1; got != want {
		t.Fatalf("store rows: %d want %d", got, want)
	}
}

// TestIssue_RejectsEmptyName — empty name is a 400 before any DB hit.
func TestIssue_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	url, _, cleanup := newServer(t, policy.Principal{UserID: "user:1"}, nil)
	defer cleanup()
	body, _ := json.Marshal(IssueRequest{Name: "   ", Scopes: []string{"read"}})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d want 400", res.StatusCode)
	}
}

// TestIssue_RejectsEmptyScopes — empty scope list is a 400.
func TestIssue_RejectsEmptyScopes(t *testing.T) {
	t.Parallel()
	url, _, cleanup := newServer(t, policy.Principal{UserID: "user:1"}, nil)
	defer cleanup()
	body, _ := json.Marshal(IssueRequest{Name: "x", Scopes: nil})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d want 400", res.StatusCode)
	}
}

// TestIssue_RejectsUnknownExpiry — only the documented presets are
// accepted; arbitrary durations are 400.
func TestIssue_RejectsUnknownExpiry(t *testing.T) {
	t.Parallel()
	url, _, cleanup := newServer(t, policy.Principal{UserID: "user:1"}, nil)
	defer cleanup()
	body, _ := json.Marshal(IssueRequest{Name: "x", Scopes: []string{"read"}, ExpiresIn: "42d"})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d want 400", res.StatusCode)
	}
}

// TestList_NoTokens_ReturnsEmpty — fresh user gets {"data":[]}.
func TestList_NoTokens_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	url, _, cleanup := newServer(t, policy.Principal{UserID: "user:1"}, nil)
	defer cleanup()
	res, err := http.Get(url + "/api/v1/me/tokens")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out struct {
		Data []TokenView `json:"data"`
	}
	decode(t, res, &out)
	if len(out.Data) != 0 {
		t.Fatalf("expected empty data, got %v", out.Data)
	}
}

// TestList_DoesNotIncludeHashOrPlaintext — guard against accidental
// leakage if someone adds a Hash/Plaintext field to TokenView later.
func TestList_DoesNotIncludeHashOrPlaintext(t *testing.T) {
	t.Parallel()
	pr := policy.Principal{UserID: "user:1"}
	url, store, cleanup := newServer(t, pr, nil)
	defer cleanup()

	_, row, hash, err := pat.New("user:1", "x", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := store.Issue(context.Background(), row, hash); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	res, err := http.Get(url + "/api/v1/me/tokens")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	low := strings.ToLower(string(body))
	if strings.Contains(low, "plaintext") {
		t.Fatalf("list response leaked plaintext field: %s", body)
	}
	if strings.Contains(low, "\"hash\"") {
		t.Fatalf("list response leaked hash field: %s", body)
	}
}

// TestRevoke_HappyPath — DELETE returns 204; subsequent list excludes
// the row.
func TestRevoke_HappyPath(t *testing.T) {
	t.Parallel()
	pr := policy.Principal{UserID: "user:1"}
	url, store, cleanup := newServer(t, pr, nil)
	defer cleanup()

	_, row, hash, err := pat.New("user:1", "x", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored, err := store.Issue(context.Background(), row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	req, _ := http.NewRequest("DELETE", url+"/api/v1/me/tokens/"+stored.ID, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d want 204", res.StatusCode)
	}
	rows, _ := store.List(context.Background(), "user:1")
	if len(rows) != 0 {
		t.Fatalf("expected 0 active tokens after revoke, got %d", len(rows))
	}
}

// TestRevoke_Unknown — 404 when the id does not exist.
func TestRevoke_Unknown(t *testing.T) {
	t.Parallel()
	url, _, cleanup := newServer(t, policy.Principal{UserID: "user:1"}, nil)
	defer cleanup()
	req, _ := http.NewRequest("DELETE", url+"/api/v1/me/tokens/00000000-0000-7000-8000-000000000999", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d want 404", res.StatusCode)
	}
}

// TestRevoke_OtherUsersToken — user can't revoke another user's
// token. The store returns ErrNotFound; the handler propagates 404
// rather than 403 to avoid leaking existence.
func TestRevoke_OtherUsersToken(t *testing.T) {
	t.Parallel()
	pr := policy.Principal{UserID: "user:1"}
	url, store, cleanup := newServer(t, pr, nil)
	defer cleanup()

	_, row, hash, err := pat.New("user:2", "x", []string{"read"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	other, err := store.Issue(context.Background(), row, hash)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	req, _ := http.NewRequest("DELETE", url+"/api/v1/me/tokens/"+other.ID, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d want 404", res.StatusCode)
	}
}

// TestGate_NoPrincipal_401 — without a Principal on the context the
// gate returns 401 instead of a panic.
func TestGate_NoPrincipal_401(t *testing.T) {
	t.Parallel()
	store := pat.NewMemoryStore()
	mux := http.NewServeMux()
	if err := Mount(mux, "/api/v1/me/tokens", Deps{
		Store: store,
		UserCaps: func(_ context.Context, _ string) (policy.CapabilitySet, error) {
			return nil, nil
		},
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/api/v1/me/tokens")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", res.StatusCode)
	}
}

// TestIssue_NeverExpiry — "never" preset yields a nil ExpiresAt.
func TestIssue_NeverExpiry(t *testing.T) {
	t.Parallel()
	pr := policy.Principal{UserID: "user:1"}
	caps := policy.NewCapabilitySet(policy.CapRead)
	url, _, cleanup := newServer(t, pr, caps)
	defer cleanup()
	body, _ := json.Marshal(IssueRequest{Name: "x", Scopes: []string{"read"}, ExpiresIn: "never"})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", res.StatusCode)
	}
	var view IssuedTokenView
	decode(t, res, &view)
	if view.ExpiresAt != nil {
		t.Fatalf("expected nil ExpiresAt for never preset, got %v", view.ExpiresAt)
	}
}

// TestIssue_30dExpiry — yields ExpiresAt ≈ now + 30d.
func TestIssue_30dExpiry(t *testing.T) {
	t.Parallel()
	pr := policy.Principal{UserID: "user:1"}
	caps := policy.NewCapabilitySet(policy.CapRead)
	url, _, cleanup := newServer(t, pr, caps)
	defer cleanup()
	body, _ := json.Marshal(IssueRequest{Name: "x", Scopes: []string{"read"}, ExpiresIn: "30d"})
	res, err := http.Post(url+"/api/v1/me/tokens", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer res.Body.Close()
	var view IssuedTokenView
	decode(t, res, &view)
	if view.ExpiresAt == nil {
		t.Fatal("expected non-nil ExpiresAt")
	}
	d := time.Until(*view.ExpiresAt)
	const margin = 5 * time.Minute
	if d < 30*24*time.Hour-margin || d > 30*24*time.Hour+margin {
		t.Fatalf("ExpiresAt out of band: %v", d)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func containsAll(xs, wants []string) bool {
	for _, w := range wants {
		if !contains(xs, w) {
			return false
		}
	}
	return true
}
