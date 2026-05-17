package audit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestEmitter_CapturesActor(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store).WithActor("user-42")

	err := e.Emit(context.Background(), "auth.login.success")
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got, _ := store.List(context.Background(), Filter{})
	if got[0].ActorUserID != "user-42" {
		t.Errorf("ActorUserID: got %q want %q", got[0].ActorUserID, "user-42")
	}
}

func TestEmitter_CapturesPlugin(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store).WithPlugin("gn-forms")

	_ = e.Emit(context.Background(), "gn-forms.submission.exported")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].ActorPluginSlug != "gn-forms" {
		t.Errorf("ActorPluginSlug: got %q want %q", got[0].ActorPluginSlug, "gn-forms")
	}
}

func TestEmitter_CapturesHTTPRequestContext(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "192.0.2.1:54321"
	req.Header.Set("User-Agent", "test-ua/1.0")

	derived := e.WithHTTP(req)
	_ = derived.Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})

	if got[0].IP != "192.0.2.1" {
		t.Errorf("IP: got %q want 192.0.2.1", got[0].IP)
	}
	if got[0].UserAgent != "test-ua/1.0" {
		t.Errorf("UA: got %q", got[0].UserAgent)
	}
}

func TestEmitter_WithHTTP_IgnoresXFFWhenNoTrustedProxies(t *testing.T) {
	// With no trusted proxies configured (the default), an XFF header
	// from a directly-connecting client must not be honored — otherwise
	// any client could spoof their source IP in the audit log.
	store := NewMemoryStore()
	e := NewEmitter(store)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.99:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "203.0.113.99" {
		t.Errorf("IP: got %q want 203.0.113.99 (RemoteAddr — XFF must be ignored without trusted proxies)", got[0].IP)
	}
}

func TestEmitter_WithHTTP_HonorsXFFWhenPeerIsTrustedProxy(t *testing.T) {
	// When the immediate peer is a trusted proxy, walk XFF from
	// rightmost to leftmost and return the first untrusted hop.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234" // proxy itself
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.2")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "203.0.113.7" {
		t.Errorf("IP: got %q want 203.0.113.7 (leftmost untrusted hop)", got[0].IP)
	}
}

func TestEmitter_WithHTTP_TrustChain_StopsAtFirstUntrustedFromRight(t *testing.T) {
	// XFF: client, attacker-claim, trusted-proxy-1, trusted-proxy-2.
	// Reading right-to-left we skip the two trusted hops and stop at
	// "attacker-claim". A naive leftmost read would honor "client" — a
	// claim the attacker fully controls. Stopping at first untrusted
	// from the right gives the closest verifiable address.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 203.0.113.99, 10.0.0.7, 10.0.0.8")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "203.0.113.99" {
		t.Errorf("IP: got %q want 203.0.113.99 (rightmost untrusted hop)", got[0].IP)
	}
}

func TestEmitter_WithHTTP_TrustChain_UntrustedPeerIgnoresXFF(t *testing.T) {
	// Trust list is configured but the peer is NOT a trusted proxy.
	// Spoofed XFF must still be ignored — only a trusted peer's XFF
	// claim is consulted.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.50:1234" // direct client, not a proxy
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "203.0.113.50" {
		t.Errorf("IP: got %q want 203.0.113.50 (untrusted peer means XFF ignored)", got[0].IP)
	}
}

func TestEmitter_WithHTTP_TrustChain_AllHopsTrustedFallsBackToPeer(t *testing.T) {
	// Every XFF hop is itself a trusted proxy. There is no untrusted
	// address to report — fall back to the immediate peer.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "10.0.0.7, 10.0.0.8")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "10.0.0.1" {
		t.Errorf("IP: got %q want 10.0.0.1 (every hop trusted; report peer)", got[0].IP)
	}
}

func TestEmitter_WithHTTP_TrustChain_NoXFFOnTrustedPeer(t *testing.T) {
	// Trusted proxy with no XFF header — report the peer itself.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "10.0.0.1" {
		t.Errorf("IP: got %q want 10.0.0.1", got[0].IP)
	}
}

func TestEmitter_WithHTTP_TrustChain_MalformedXFFEntry(t *testing.T) {
	// An unparseable hop in XFF: treat it as untrusted and report it
	// verbatim. The audit row is best-effort and a fuzzy value is more
	// honest than silently falling back to the proxy.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "not-an-ip, 10.0.0.7")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "not-an-ip" {
		t.Errorf("IP: got %q want \"not-an-ip\" (malformed hop reported verbatim)", got[0].IP)
	}
}

func TestEmitter_WithHTTP_TrustChain_IPv6Proxy(t *testing.T) {
	// IPv6 trusted proxy with IPv4 client in XFF.
	store := NewMemoryStore()
	trusted := []netip.Prefix{netip.MustParsePrefix("fd00::/8")}
	e := NewEmitter(store).WithTrustedProxies(trusted)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "[fd12::1]:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "203.0.113.42" {
		t.Errorf("IP: got %q want 203.0.113.42", got[0].IP)
	}
}

func TestEmitter_WithTrustedProxies_DoesNotMutateParent(t *testing.T) {
	store := NewMemoryStore()
	root := NewEmitter(store)
	_ = root.WithTrustedProxies([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})

	if len(root.TrustedProxies()) != 0 {
		t.Errorf("root mutated: TrustedProxies=%v", root.TrustedProxies())
	}
}

func TestEmitter_TrustedProxies_ReturnsCopy(t *testing.T) {
	store := NewMemoryStore()
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	e := NewEmitter(store).WithTrustedProxies(prefixes)

	got := e.TrustedProxies()
	if len(got) != 1 {
		t.Fatalf("len: got %d want 1", len(got))
	}
	// Mutating the returned slice must not affect the emitter.
	got[0] = netip.MustParsePrefix("0.0.0.0/0")
	if e.TrustedProxies()[0].String() == "0.0.0.0/0" {
		t.Error("TrustedProxies leaked internal slice")
	}
}

func TestEmitter_WithHTTP_BadRemoteAddr(t *testing.T) {
	// RemoteAddr without a port (rare; usually a hijacked conn). The
	// clientIP should still produce a usable string — no panic, no
	// silent zero value.
	store := NewMemoryStore()
	e := NewEmitter(store)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "weirdvalue" // no host:port

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "weirdvalue" {
		t.Errorf("IP: got %q want weirdvalue", got[0].IP)
	}
}

func TestEmitter_WithRequest_CombinesActorAndHTTP(t *testing.T) {
	store := NewMemoryStore()
	root := NewEmitter(store)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "198.51.100.9:9999"
	req.Header.Set("User-Agent", "ua")

	e := WithRequest(root, req, "user-7")
	_ = e.Emit(context.Background(), "post.published",
		WithTarget("post", "p-99"),
		WithMetadata(map[string]any{"slug": "hello"}),
		WithSeverity(SeverityWarning),
	)
	got, _ := store.List(context.Background(), Filter{})
	if got[0].ActorUserID != "user-7" {
		t.Errorf("ActorUserID: got %q", got[0].ActorUserID)
	}
	if got[0].IP != "198.51.100.9" {
		t.Errorf("IP: got %q", got[0].IP)
	}
	if got[0].ResourceType != "post" || got[0].ResourceID != "p-99" {
		t.Errorf("target: got %q/%q", got[0].ResourceType, got[0].ResourceID)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("severity: got %q", got[0].Severity)
	}
	if got[0].Metadata["slug"] != "hello" {
		t.Errorf("metadata: got %+v", got[0].Metadata)
	}
}

func TestEmitter_Derived_DoesNotMutateParent(t *testing.T) {
	store := NewMemoryStore()
	root := NewEmitter(store)
	_ = root.WithActor("derived-user")

	_ = root.Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].ActorUserID != "" {
		t.Errorf("root mutated: ActorUserID=%q", got[0].ActorUserID)
	}
}

func TestEmitter_Options_Compose(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store)

	_ = e.Emit(context.Background(), "x.y",
		WithMetadata(map[string]any{"a": 1, "b": 2}),
		WithMetadata(map[string]any{"b": 3, "c": 4}), // later wins
	)
	got, _ := store.List(context.Background(), Filter{})
	if got[0].Metadata["a"] != 1 || got[0].Metadata["b"] != 3 || got[0].Metadata["c"] != 4 {
		t.Errorf("metadata merge: got %+v", got[0].Metadata)
	}
}

func TestEmitter_RejectsEmptyEventType(t *testing.T) {
	e := NewEmitter(NewMemoryStore())
	err := e.Emit(context.Background(), "")
	if !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("expected ErrInvalidEvent, got %v", err)
	}
}

func TestEmitter_ActorOverride(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store).WithActor("default")
	_ = e.Emit(context.Background(), "x.y", WithActorOverride("override"))
	got, _ := store.List(context.Background(), Filter{})
	if got[0].ActorUserID != "override" {
		t.Errorf("override: got %q", got[0].ActorUserID)
	}
}

func TestEmitter_IPOverride(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store)
	_ = e.Emit(context.Background(), "x.y", WithIP("127.0.0.1"))
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "127.0.0.1" {
		t.Errorf("IP: got %q", got[0].IP)
	}
}

func TestNewEmitter_PanicsOnNilStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	NewEmitter(nil)
}

func TestEmitter_StoreAccessor(t *testing.T) {
	s := NewMemoryStore()
	e := NewEmitter(s)
	if e.Store() != s {
		t.Error("Store() did not return the underlying store")
	}
}
