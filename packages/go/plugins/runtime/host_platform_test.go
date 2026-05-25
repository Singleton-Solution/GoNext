package runtime

import (
	"context"
	"sync"
	"testing"
)

// emitterCapture is an AuditEmitterFunc that records every emission
// so the test can assert on the platform.* rows the runtime writes.
type emitterCapture struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	pluginSlug string
	event      string
	meta       map[string]any
}

func (e *emitterCapture) Emit(_ context.Context, slug, event string, meta map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Copy the meta map so subsequent mutations on the caller side
	// don't change what we captured.
	cp := make(map[string]any, len(meta))
	for k, v := range meta {
		cp[k] = v
	}
	e.events = append(e.events, capturedEvent{pluginSlug: slug, event: event, meta: cp})
	return nil
}

func (e *emitterCapture) get() []capturedEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]capturedEvent, len(e.events))
	copy(out, e.events)
	return out
}

func TestWithPlatform_NilSafe(t *testing.T) {
	// A platform configured with no services should be a no-op:
	// New() succeeds, no exports added, env module still works.
	rt, err := New(context.Background(),
		WithPlatform(PlatformConfig{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	if rt.platform == nil {
		t.Errorf("platform should be set even with empty config")
	}
}

func TestPlatformContext_ResolveSlug(t *testing.T) {
	// No mapper → moduleName passes through.
	p := &platformContext{}
	if got := p.resolveSlug("seo"); got != "seo" {
		t.Errorf("default resolveSlug = %q, want %q", got, "seo")
	}
	// Mapper returning empty → fall back to moduleName.
	p.slugFor = func(string) string { return "" }
	if got := p.resolveSlug("seo"); got != "seo" {
		t.Errorf("empty mapper fallback failed: got %q", got)
	}
	// Mapper returning a value → use it.
	p.slugFor = func(name string) string { return name + "-mapped" }
	if got := p.resolveSlug("seo"); got != "seo-mapped" {
		t.Errorf("mapper result not used: got %q", got)
	}
}

func TestPlatformContext_EmitPlatform_NilEmitter(t *testing.T) {
	// emitPlatform on a context with nil emitter must not panic.
	p := &platformContext{}
	p.emitPlatform(context.Background(), nil, "seo", "test.event", nil)
}

func TestPlatformContext_EmitPlatform_HappyPath(t *testing.T) {
	cap := &emitterCapture{}
	p := &platformContext{platformEmitter: cap}
	p.emitPlatform(context.Background(), nil, "seo", "plugin.seo.platform.secrets.get",
		map[string]any{"key": "api-token", "result": "ok"})

	events := cap.get()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].pluginSlug != "seo" {
		t.Errorf("slug: got %q, want %q", events[0].pluginSlug, "seo")
	}
	if events[0].event != "plugin.seo.platform.secrets.get" {
		t.Errorf("event: got %q", events[0].event)
	}
	if events[0].meta["key"] != "api-token" {
		t.Errorf("meta[key]: got %v", events[0].meta["key"])
	}
}
