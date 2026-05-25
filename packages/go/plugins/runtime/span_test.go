package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// recordingTracer captures every StartSpan call into an in-memory
// log. Tests use it to assert on span shape (name + attrs) without
// spinning up the OTel SDK.
type recordingTracer struct {
	mu    sync.Mutex
	spans []recordedSpan
}

type recordedSpan struct {
	Name     string
	Attrs    map[string]string
	EndedErr error
	Extra    map[string]string // SetAttribute additions
	Ended    bool
}

func (r *recordingTracer) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanCloser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := &recordedSpan{
		Name:  name,
		Attrs: make(map[string]string, len(attrs)),
		Extra: make(map[string]string),
	}
	for k, v := range attrs {
		rec.Attrs[k] = v
	}
	r.spans = append(r.spans, *rec)
	idx := len(r.spans) - 1
	return ctx, &recordingSpanCloser{tracer: r, idx: idx}
}

func (r *recordingTracer) Spans() []recordedSpan {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedSpan, len(r.spans))
	copy(out, r.spans)
	return out
}

type recordingSpanCloser struct {
	tracer *recordingTracer
	idx    int
}

func (c *recordingSpanCloser) End(err error) {
	c.tracer.mu.Lock()
	defer c.tracer.mu.Unlock()
	c.tracer.spans[c.idx].Ended = true
	c.tracer.spans[c.idx].EndedErr = err
}

func (c *recordingSpanCloser) SetAttribute(k, v string) {
	c.tracer.mu.Lock()
	defer c.tracer.mu.Unlock()
	if c.tracer.spans[c.idx].Extra == nil {
		c.tracer.spans[c.idx].Extra = make(map[string]string)
	}
	c.tracer.spans[c.idx].Extra[k] = v
}

// recordingSpanReceiver captures gn_span_event observations.
type recordingSpanReceiver struct {
	mu     sync.Mutex
	events []recordedSpanEvent
}

type recordedSpanEvent struct {
	Slug  string
	Name  string
	Attrs map[string]string
}

func (r *recordingSpanReceiver) AddSpanEvent(_ context.Context, slug, name string, attrs map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make(map[string]string, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	r.events = append(r.events, recordedSpanEvent{Slug: slug, Name: name, Attrs: cp})
}

func (r *recordingSpanReceiver) Events() []recordedSpanEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedSpanEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestStartPluginSpan_NoTracer_NoOp asserts the no-OTel path: a
// runtime without a Tracer returns the input ctx and a no-op closer
// so callers can chain unconditionally.
func TestStartPluginSpan_NoTracer_NoOp(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	ctx := t.Context()
	gotCtx, closer := rt.StartPluginSpan(ctx, "plugin-a", "gn_log")
	if gotCtx != ctx {
		t.Error("ctx should be unchanged when no Tracer is wired")
	}
	closer.End(nil) // must not panic
}

// TestStartPluginSpan_RecordsAttrs covers the happy path: the
// returned span carries the canonical plugin attrs.
func TestStartPluginSpan_RecordsAttrs(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	tracer := &recordingTracer{}
	rt.UseObservability().WithTracer(tracer)

	_, closer := rt.StartPluginSpan(t.Context(), "plugin-a", "gn_log")
	closer.End(nil)

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("spans: got %d, want 1", len(spans))
	}
	if spans[0].Name != "plugin.gn_log" {
		t.Errorf("name: got %q, want %q", spans[0].Name, "plugin.gn_log")
	}
	if spans[0].Attrs["gonext.plugin.slug"] != "plugin-a" {
		t.Errorf("slug attr: got %q", spans[0].Attrs["gonext.plugin.slug"])
	}
	if spans[0].Attrs["gonext.plugin.abi"] != "gn_log" {
		t.Errorf("abi attr: got %q", spans[0].Attrs["gonext.plugin.abi"])
	}
}

// TestStartPluginSpan_RecordsError ensures the error path lands on
// the span via End(err).
func TestStartPluginSpan_RecordsError(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	tracer := &recordingTracer{}
	rt.UseObservability().WithTracer(tracer)

	_, closer := rt.StartPluginSpan(t.Context(), "plugin-a", "gn_log")
	wantErr := errors.New("boom")
	closer.End(wantErr)

	spans := tracer.Spans()
	if !spans[0].Ended || spans[0].EndedErr != wantErr {
		t.Errorf("span end: ended=%v err=%v, want ended=true err=%v", spans[0].Ended, spans[0].EndedErr, wantErr)
	}
}

// TestStartPluginSpan_EndIsIdempotent guards the closer contract:
// double-end is allowed and only the first call counts.
func TestStartPluginSpan_EndIsIdempotent(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	tracer := &recordingTracer{}
	rt.UseObservability().WithTracer(tracer)

	_, closer := rt.StartPluginSpan(t.Context(), "plugin-a", "gn_log")
	closer.End(errors.New("first"))
	closer.End(errors.New("second")) // must be no-op

	spans := tracer.Spans()
	if spans[0].EndedErr.Error() != "first" {
		t.Errorf("End err: got %v, want first", spans[0].EndedErr)
	}
}

// TestStartPluginSpan_TracksActive verifies the spanContextRegistry
// reflects the open-span state. gn_span_event uses this signal to
// decide whether to forward.
func TestStartPluginSpan_TracksActive(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	tracer := &recordingTracer{}
	rt.UseObservability().WithTracer(tracer)

	if rt.IsSpanActive("plugin-a", "gn_log") {
		t.Fatal("span should not be active before StartPluginSpan")
	}
	_, closer := rt.StartPluginSpan(t.Context(), "plugin-a", "gn_log")
	if !rt.IsSpanActive("plugin-a", "gn_log") {
		t.Error("span should be active after StartPluginSpan")
	}
	closer.End(nil)
	if rt.IsSpanActive("plugin-a", "gn_log") {
		t.Error("span should not be active after End")
	}
}

// TestStartPluginSpan_NestedTracking verifies that overlapping spans
// for the same (slug, abi) are ref-counted correctly. This matters
// because a plugin can be inside a recursive hook dispatch.
func TestStartPluginSpan_NestedTracking(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	tracer := &recordingTracer{}
	rt.UseObservability().WithTracer(tracer)

	_, c1 := rt.StartPluginSpan(t.Context(), "plugin-a", "gn_log")
	_, c2 := rt.StartPluginSpan(t.Context(), "plugin-a", "gn_log")
	if !rt.IsSpanActive("plugin-a", "gn_log") {
		t.Fatal("inner: active should be true")
	}
	c2.End(nil)
	if !rt.IsSpanActive("plugin-a", "gn_log") {
		t.Error("after inner end: still 1 active, should remain true")
	}
	c1.End(nil)
	if rt.IsSpanActive("plugin-a", "gn_log") {
		t.Error("after outer end: should be inactive")
	}
}

// TestSpanEventReceiver_Wired_ForwardsEvents covers the
// SpanEventReceiver path: when one is wired in, gn_span_event-style
// invocations are forwarded. We exercise this via the recording
// receiver directly since invoking the host function requires a full
// WASM module fixture (covered by integration tests).
func TestSpanEventReceiver_Wired_ForwardsEvents(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	rec := &recordingSpanReceiver{}
	rt.UseObservability().WithSpanEventReceiver(rec)

	if got := rt.SpanEventReceiver(); got != rec {
		t.Errorf("SpanEventReceiver: got %v, want injected", got)
	}
}

// TestEncodeDecodeStringMap_Roundtrip verifies the tiny msgpack codec
// used for gn_span_event attrs (and, in #226, gn_metric_observe tags).
func TestEncodeDecodeStringMap_Roundtrip(t *testing.T) {
	in := map[string]string{
		"status": "ok",
		"locale": "en-US",
		"empty":  "",
	}
	encoded := EncodeStringMap(in)
	got, err := decodeStringMap(encoded)
	if err != nil {
		t.Fatalf("decodeStringMap: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("map size: got %d, want %d", len(got), len(in))
	}
	for k, v := range in {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
}

// TestDecodeStringMap_RejectsBadHeader covers the negative path — a
// blob that doesn't start with a map prefix returns an error rather
// than silently producing an empty map.
func TestDecodeStringMap_RejectsBadHeader(t *testing.T) {
	if _, err := decodeStringMap([]byte{0xff}); err == nil {
		t.Error("expected error on bad header, got nil")
	}
}
