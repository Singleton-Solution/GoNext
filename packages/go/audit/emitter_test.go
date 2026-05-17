package audit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestEmitter_WithHTTP_HonorsXForwardedFor(t *testing.T) {
	store := NewMemoryStore()
	e := NewEmitter(store)

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")

	_ = e.WithHTTP(req).Emit(context.Background(), "x.y")
	got, _ := store.List(context.Background(), Filter{})
	if got[0].IP != "203.0.113.7" {
		t.Errorf("IP: got %q want 203.0.113.7", got[0].IP)
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
