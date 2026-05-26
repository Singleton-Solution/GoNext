package safehttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeResolverAny is the same idea as fakeResolver from ssrf_test.go
// but with a fallback "everything resolves to 8.8.8.8 if in
// publicSet, otherwise to loopback" behavior — combined with
// WithDialContext, this funnels every test call to the httptest server
// while still exercising the SSRF code path.
type fakeResolverAny struct {
	addr netip.Addr
	// publicSet is the set of names that should pass the SSRF guard
	// (resolve to 8.8.8.8 instead of the test loopback). Anything not
	// here resolves to the fallback (loopback) — combined with
	// WithDialContext that funnels the call to the test server, which
	// is what most successful-case tests want.
	publicSet map[string]bool
}

func (f *fakeResolverAny) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if f.publicSet[host] {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	// Default: loopback. The SSRF guard would reject this — only the
	// publicSet entries pass.
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

// resolveSuccess sets up a Client that points every allowed host at
// the test server. SSRF gets bypassed by reporting 8.8.8.8 for the
// host even though the dial goes to the test loopback.
func clientForServer(t *testing.T, srv *httptest.Server, hosts ...string) *Client {
	t.Helper()
	publicSet := map[string]bool{}
	for _, h := range hosts {
		publicSet[h] = true
	}
	c, err := New(
		WithAllowlist(hosts...),
		WithResolver(&fakeResolverAny{publicSet: publicSet}),
		WithDialContext(func(_ context.Context, network, _ string) (net.Conn, error) {
			// Always dial the test server's listener regardless of the
			// dial target. This is the standard httptest trick.
			return net.Dial(network, srv.Listener.Addr().String())
		}),
		WithSchemes("http", "https"),
		WithTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNew_RequiresAllowlist(t *testing.T) {
	t.Parallel()
	if _, err := New(); err == nil {
		t.Errorf("expected error when allowlist is empty")
	}
	if _, err := New(WithAllowlist("api.example.com")); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_Blocks_NonAllowlistHost(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	c := clientForServer(t, srv, "api.example.com")

	_, err := c.Get(context.Background(), "https://other.example.com/")
	if err == nil {
		t.Fatalf("expected ErrBlocked")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("want ErrBlocked, got %v", err)
	}
}

func TestClient_Blocks_BadScheme(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(srv.Close)
	c := clientForServer(t, srv, "api.example.com")

	_, err := c.Get(context.Background(), "file:///etc/passwd")
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Errorf("expected ErrBlocked for file://, got %v", err)
	}
}

func TestClient_Blocks_PrivateIP(t *testing.T) {
	t.Parallel()
	c, err := New(
		WithAllowlist("10.0.0.1"),
		WithSchemes("http", "https"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 10.0.0.1 is in the allowlist by exact host but the SSRF guard
	// rejects it because it's RFC1918.
	_, err = c.Get(context.Background(), "http://10.0.0.1/")
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Errorf("expected ErrBlocked for private IP, got %v", err)
	}
}

func TestClient_Blocks_LoopbackHostname(t *testing.T) {
	t.Parallel()
	c, err := New(
		WithAllowlist("evil.example.com"),
		WithResolver(&fakeResolver{mapping: map[string][]netip.Addr{
			"evil.example.com": {netip.MustParseAddr("127.0.0.1")},
		}}),
		WithSchemes("http", "https"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), "http://evil.example.com/")
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Errorf("expected ErrBlocked for hostname->127.0.0.1, got %v", err)
	}
}

func TestClient_Allows_PublicHost(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(srv.Close)
	c := clientForServer(t, srv, "api.example.com")

	resp, err := c.Get(context.Background(), "http://api.example.com/foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body=%q want %q", body, "hello")
	}
}

func TestClient_MaxResponseBytes_Truncates(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		WithAllowlist("api.example.com"),
		WithResolver(&fakeResolverAny{publicSet: map[string]bool{"api.example.com": true}}),
		WithDialContext(func(_ context.Context, network, _ string) (net.Conn, error) {
			return net.Dial(network, srv.Listener.Addr().String())
		}),
		WithSchemes("http", "https"),
		WithMaxResponseBytes(256),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := c.Get(context.Background(), "http://api.example.com/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(body) != 256 {
		t.Errorf("body len=%d want 256", len(body))
	}
}

func TestClient_RedirectCap(t *testing.T) {
	t.Parallel()
	// Build a server that always 302s to itself. The redirect cap
	// should kick in after 3 hops.
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/loop?h="+strings.Repeat("x", hits), http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := clientForServer(t, srv, "api.example.com")

	_, err := c.Get(context.Background(), "http://api.example.com/loop")
	if err == nil {
		t.Fatalf("expected redirect-cap error")
	}
	if !strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "Redirect") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestClient_RedirectRevalidates(t *testing.T) {
	t.Parallel()
	// Server that 302s to a host outside the allowlist.
	mux := http.NewServeMux()
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://blocked.example.com/foo", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(
		WithAllowlist("api.example.com"),
		WithResolver(&fakeResolverAny{publicSet: map[string]bool{
			"api.example.com":     true,
			"blocked.example.com": true,
		}}),
		WithDialContext(func(_ context.Context, network, _ string) (net.Conn, error) {
			return net.Dial(network, srv.Listener.Addr().String())
		}),
		WithSchemes("http", "https"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), "http://api.example.com/redir")
	if err == nil {
		t.Fatalf("expected redirect-blocked error")
	}
	if !strings.Contains(err.Error(), "allowlist") && !errors.Is(err, ErrBlocked) {
		t.Errorf("unexpected error %v", err)
	}
}

func TestClient_AllowsSubdomains(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		WithAllowlist("example.com"),
		WithAllowlistSubdomains(),
		WithResolver(&fakeResolverAny{publicSet: map[string]bool{
			"api.example.com": true,
		}}),
		WithDialContext(func(_ context.Context, network, _ string) (net.Conn, error) {
			return net.Dial(network, srv.Listener.Addr().String())
		}),
		WithSchemes("http", "https"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Subdomain allowed.
	resp, err := c.Get(context.Background(), "http://api.example.com/")
	if err != nil {
		t.Fatalf("subdomain rejected: %v", err)
	}
	resp.Body.Close()

	// Evil twin not allowed: "evilexample.com" should NOT match
	// "example.com" because the suffix isn't preceded by '.'.
	c2, err := New(
		WithAllowlist("example.com"),
		WithAllowlistSubdomains(),
		WithSchemes("http", "https"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c2.Allows("evilexample.com") {
		t.Errorf("evilexample.com should not match example.com")
	}
}

func TestClient_Timeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		WithAllowlist("api.example.com"),
		WithResolver(&fakeResolverAny{publicSet: map[string]bool{"api.example.com": true}}),
		WithDialContext(func(_ context.Context, network, _ string) (net.Conn, error) {
			return net.Dial(network, srv.Listener.Addr().String())
		}),
		WithSchemes("http", "https"),
		WithTimeout(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), "http://api.example.com/")
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Errorf("expected url.Error, got %T %v", err, err)
	}
}

