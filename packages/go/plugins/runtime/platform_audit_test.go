package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

// auditCapture is an AuditEmitterFunc that records every emission
// for assertion.
type auditCapture struct {
	events []capturedEvent
	emitErr error
}

func (a *auditCapture) Emit(_ context.Context, slug, event string, meta map[string]any) error {
	if a.emitErr != nil {
		return a.emitErr
	}
	cp := make(map[string]any, len(meta))
	for k, v := range meta {
		cp[k] = v
	}
	a.events = append(a.events, capturedEvent{pluginSlug: slug, event: event, meta: cp})
	return nil
}

func TestAuditSink_SlugPrefix_HappyPath(t *testing.T) {
	cap := &auditCapture{}
	sink := NewAuditSink(cap, AuditSinkConfig{PerPluginPerMinute: 100})
	if err := sink.Emit(context.Background(), "seo", "seo.sitemap.regen", nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(cap.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(cap.events))
	}
	if cap.events[0].event != "plugin.seo.sitemap.regen" {
		t.Errorf("event name = %q, want %q", cap.events[0].event, "plugin.seo.sitemap.regen")
	}
	if cap.events[0].pluginSlug != "seo" {
		t.Errorf("slug = %q", cap.events[0].pluginSlug)
	}
}

func TestAuditSink_SlugPrefix_AllowsBareSlug(t *testing.T) {
	cap := &auditCapture{}
	sink := NewAuditSink(cap, AuditSinkConfig{PerPluginPerMinute: 100})
	if err := sink.Emit(context.Background(), "seo", "seo", nil); err != nil {
		t.Fatalf("Emit bare slug: %v", err)
	}
	if cap.events[0].event != "plugin.seo" {
		t.Errorf("event = %q", cap.events[0].event)
	}
}

func TestAuditSink_SlugPrefix_StripsPluginPrefix(t *testing.T) {
	cap := &auditCapture{}
	sink := NewAuditSink(cap, AuditSinkConfig{PerPluginPerMinute: 100})
	// Plugin author who mirrored the host convention should still
	// succeed; we strip the prefix and re-add it.
	if err := sink.Emit(context.Background(), "seo", "plugin.seo.foo", nil); err != nil {
		t.Fatalf("Emit with plugin prefix: %v", err)
	}
	if cap.events[0].event != "plugin.seo.foo" {
		t.Errorf("event = %q", cap.events[0].event)
	}
}

func TestAuditSink_SlugPrefix_RejectsOtherPlugin(t *testing.T) {
	cap := &auditCapture{}
	sink := NewAuditSink(cap, AuditSinkConfig{PerPluginPerMinute: 100})
	err := sink.Emit(context.Background(), "seo", "other-plugin.event", nil)
	if !errors.Is(err, ErrAuditSlugPrefix) {
		t.Errorf("want ErrAuditSlugPrefix, got %v", err)
	}
	if len(cap.events) != 0 {
		t.Errorf("rejected emit must not write: got %d events", len(cap.events))
	}
}

func TestAuditSink_SlugPrefix_RejectsPrefixWithoutDot(t *testing.T) {
	// "seofoo" matches "seo" as a string prefix but isn't a real
	// dot-separated namespace. Must reject.
	cap := &auditCapture{}
	sink := NewAuditSink(cap, AuditSinkConfig{PerPluginPerMinute: 100})
	err := sink.Emit(context.Background(), "seo", "seofoo.bar", nil)
	if !errors.Is(err, ErrAuditSlugPrefix) {
		t.Errorf("want ErrAuditSlugPrefix, got %v", err)
	}
}

func TestAuditSink_EmptyEvent(t *testing.T) {
	cap := &auditCapture{}
	sink := NewAuditSink(cap, AuditSinkConfig{PerPluginPerMinute: 100})
	if err := sink.Emit(context.Background(), "seo", "", nil); !errors.Is(err, ErrAuditEmpty) {
		t.Errorf("want ErrAuditEmpty, got %v", err)
	}
}

func TestAuditSink_RateLimit(t *testing.T) {
	cap := &auditCapture{}
	now := time.Now()
	sink := NewAuditSink(cap, AuditSinkConfig{
		PerPluginPerMinute: 3,
		Burst:              3,
		NowFunc:            func() time.Time { return now },
	})
	// 3 allowed, 4th rejected.
	for i := 0; i < 3; i++ {
		if err := sink.Emit(context.Background(), "seo", "seo.event", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	err := sink.Emit(context.Background(), "seo", "seo.event", nil)
	if !errors.Is(err, ErrAuditRateLimited) {
		t.Errorf("want ErrAuditRateLimited, got %v", err)
	}
	if len(cap.events) != 3 {
		t.Errorf("rate-limited call must not write: got %d events", len(cap.events))
	}
}

func TestAuditSink_RateLimit_Refill(t *testing.T) {
	cap := &auditCapture{}
	now := time.Now()
	sink := NewAuditSink(cap, AuditSinkConfig{
		PerPluginPerMinute: 60, // 1 per second
		Burst:              1,
		NowFunc:            func() time.Time { return now },
	})
	// Burn the single burst token.
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); err != nil {
		t.Fatalf("first emit: %v", err)
	}
	// Same instant — bucket empty.
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); !errors.Is(err, ErrAuditRateLimited) {
		t.Errorf("want rate limit, got %v", err)
	}
	// Advance one second — one token refilled.
	now = now.Add(time.Second)
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); err != nil {
		t.Errorf("after refill: %v", err)
	}
}

func TestAuditSink_RateLimit_IndependentPerPlugin(t *testing.T) {
	cap := &auditCapture{}
	now := time.Now()
	sink := NewAuditSink(cap, AuditSinkConfig{
		PerPluginPerMinute: 1,
		Burst:              1,
		NowFunc:            func() time.Time { return now },
	})
	// Burn seo's token.
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); err != nil {
		t.Fatalf("seo: %v", err)
	}
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); !errors.Is(err, ErrAuditRateLimited) {
		t.Errorf("seo second: want rate limit, got %v", err)
	}
	// Different plugin — independent bucket.
	if err := sink.Emit(context.Background(), "other", "other.event", nil); err != nil {
		t.Errorf("other: %v", err)
	}
}

func TestAuditSink_EmitterFailure_TokenNotRefunded(t *testing.T) {
	cap := &auditCapture{emitErr: errors.New("store down")}
	now := time.Now()
	sink := NewAuditSink(cap, AuditSinkConfig{
		PerPluginPerMinute: 60,
		Burst:              1,
		NowFunc:            func() time.Time { return now },
	})
	// Burn the token through a failing emit.
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); err == nil {
		t.Fatalf("expected emitter error")
	}
	// Cap is now empty (we don't refund on emitter failure).
	if err := sink.Emit(context.Background(), "seo", "seo.event", nil); !errors.Is(err, ErrAuditRateLimited) {
		t.Errorf("want rate limit (no refund), got %v", err)
	}
}

func TestNewAuditSink_NilEmitter(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for nil emitter")
		}
	}()
	NewAuditSink(nil, AuditSinkConfig{})
}

func TestTokenBucket_BasicBurst(t *testing.T) {
	now := time.Now()
	b := newTokenBucket(60, 5, func() time.Time { return now })
	for i := 0; i < 5; i++ {
		if !b.take() {
			t.Errorf("burst take %d: failed", i)
		}
	}
	if b.take() {
		t.Errorf("6th take should fail")
	}
}
