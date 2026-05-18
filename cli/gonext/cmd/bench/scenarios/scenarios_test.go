package scenarios

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recordingHandler captures every URL the scenario hits. It is the
// fixture for "scenarios issue the expected paths" tests.
type recordingHandler struct {
	mu    sync.Mutex
	paths []string
	bodies []string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.paths = append(h.paths, r.URL.Path+queryOf(r))
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		h.bodies = append(h.bodies, string(b))
	}
	h.mu.Unlock()
	// Vary the content type per scenario.
	switch {
	case strings.HasPrefix(r.URL.Path, "/wp-json"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	case strings.HasPrefix(r.URL.Path, "/api/v1/auth/login"):
		w.Header().Set("Content-Type", "application/json")
		// Return 200 for both branches — the scenario does its own
		// branching by request order, not response.
		_, _ = w.Write([]byte(`{"ok":true}`))
	default:
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}
}

func queryOf(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}

func TestHomepage_HitsRoot(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()
	res := Homepage{}.Iter(context.Background(), DefaultClient(), srv.URL)
	if res.Err != nil {
		t.Fatalf("Iter: %v", res.Err)
	}
	if res.Status != 200 {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if len(h.paths) != 1 || h.paths[0] != "/" {
		t.Errorf("homepage paths = %v, want [/]", h.paths)
	}
}

func TestPosts_HitsWPRestList(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()
	res := Posts{}.Iter(context.Background(), DefaultClient(), srv.URL)
	if res.Err != nil {
		t.Fatalf("Iter: %v", res.Err)
	}
	want := "/wp-json/wp/v2/posts?per_page=20"
	if len(h.paths) != 1 || h.paths[0] != want {
		t.Errorf("posts paths = %v, want [%s]", h.paths, want)
	}
}

func TestLogin_HitsTwoBranches(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()
	res := Login{}.Iter(context.Background(), DefaultClient(), srv.URL)
	if res.Err != nil {
		t.Fatalf("Iter: %v", res.Err)
	}
	if len(h.paths) != 2 {
		t.Fatalf("login paths = %v, want 2 requests", h.paths)
	}
	if h.paths[0] != "/api/v1/auth/login" || h.paths[1] != "/api/v1/auth/login" {
		t.Errorf("login paths unexpected: %v", h.paths)
	}
	// First body must be the invalid creds, second the valid creds.
	if !strings.Contains(h.bodies[0], "nobody@example.invalid") {
		t.Errorf("first login body did not contain invalid email: %q", h.bodies[0])
	}
	if !strings.Contains(h.bodies[1], "admin@example.com") {
		t.Errorf("second login body did not contain valid email: %q", h.bodies[1])
	}
	// Bodies should be valid JSON.
	for i, b := range h.bodies {
		var v map[string]string
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			t.Errorf("login body %d is not valid JSON: %v (%q)", i, err, b)
		}
	}
}

func TestLogin_CredentialOverride(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()
	t.Setenv("GONEXT_BENCH_LOGIN_VALID_EMAIL", "ci@example.com")
	t.Setenv("GONEXT_BENCH_LOGIN_VALID_PASSWORD", "ci-secret")
	res := Login{}.Iter(context.Background(), DefaultClient(), srv.URL)
	if res.Err != nil {
		t.Fatalf("Iter: %v", res.Err)
	}
	if !strings.Contains(h.bodies[1], "ci@example.com") {
		t.Errorf("override did not take effect: %q", h.bodies[1])
	}
}

func TestRestShim_CyclesThroughMix(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()
	s := &RestShim{}
	// Run one iteration per query so we see the full mix.
	for i := 0; i < len(restShimQueries); i++ {
		res := s.Iter(context.Background(), DefaultClient(), srv.URL)
		if res.Err != nil {
			t.Fatalf("Iter %d: %v", i, res.Err)
		}
		if res.Status != 200 {
			t.Errorf("Iter %d status = %d, want 200", i, res.Status)
		}
	}
	if len(h.paths) != len(restShimQueries) {
		t.Fatalf("recorded %d paths, want %d", len(h.paths), len(restShimQueries))
	}
	for i, want := range restShimQueries {
		if h.paths[i] != want {
			t.Errorf("path[%d] = %q, want %q", i, h.paths[i], want)
		}
	}
}

func TestAll_ReturnsStableOrder(t *testing.T) {
	want := []string{"homepage", "posts", "login", "restshim"}
	got := []string{}
	for _, s := range All() {
		got = append(got, s.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("All() order = %v, want %v", got, want)
	}
}

func TestSetup_RespectsContextCancel(t *testing.T) {
	// All built-in setups are no-ops; we just want to make sure none
	// of them panic on a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, s := range All() {
		if err := s.Setup(ctx, "http://example.test"); err != nil {
			t.Errorf("%s.Setup(cancelled) returned %v, want nil", s.Name(), err)
		}
	}
}
