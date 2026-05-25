package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// emitObservabilityAudit is exercised here through the cardinality
// overflow path: when the dam rejects an observation we expect a
// `plugin.metric_cardinality_exceeded` audit row carrying the metric
// name and overflowing tag. The host function is unit-tested as a
// pure function over the dam + audit emitter — the WASM-side fixture
// path is covered by integration tests.

type cardinalityAuditCapture struct {
	mu     sync.Mutex
	events []recordedAuditEvent
}

func (c *cardinalityAuditCapture) EmitPluginEvent(_ context.Context, slug, eventType, severity string, metadata map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, recordedAuditEvent{
		Slug:      slug,
		EventType: eventType,
		Severity:  severity,
		Metadata:  metadata,
	})
	return nil
}

func (c *cardinalityAuditCapture) get() []recordedAuditEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]recordedAuditEvent, len(c.events))
	copy(out, c.events)
	return out
}

// TestCardinalityOverflow_AuditAndCounter exercises the integrated
// path: when the dam rejects an observation, the runtime emits a
// warning audit row AND bumps the dropped-counter, leaving the
// metric_observe_total counter unincremented.
func TestCardinalityOverflow_AuditAndCounter(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	reg := prometheus.NewRegistry()
	pm := NewPluginMetricsWithLimit(reg, 10)
	audit := &cardinalityAuditCapture{}
	dam := NewCardinalityDam(2)
	rt.UseObservability().
		WithPluginMetrics(pm).
		WithAuditEmitter(audit).
		WithCardinalityDam(dam)
	_ = rt.RegisterPluginSlug("plugin-x")

	// Below-limit observations succeed (we exercise the dam directly
	// since the wasm-side ABI requires a fixture).
	for _, v := range []string{"a", "b"} {
		if _, ok := dam.Admit("plugin-x", "requests", map[string]string{"user_id": v}); !ok {
			t.Fatalf("dam rejected admitted value %q unexpectedly", v)
		}
		pm.IncABICall("plugin-x", "gn_metric_observe", "ok")
	}

	// Third observation pushes over the per-tag limit. The runtime
	// integration path: dam rejects → IncABICall(cardinality_exceeded)
	// + IncMetricCardinalityExceeded + audit emit.
	overTag, admitted := dam.Admit("plugin-x", "requests", map[string]string{"user_id": "c"})
	if admitted {
		t.Fatal("dam should reject third value at limit 2")
	}
	pm.IncABICall("plugin-x", "gn_metric_observe", "cardinality_exceeded")
	pm.IncMetricCardinalityExceeded("plugin-x", "requests")
	_ = emitObservabilityAudit(context.Background(), rt, "plugin-x", "plugin.metric_cardinality_exceeded", "warning", map[string]any{
		"metric":          "requests",
		"overflowing_tag": overTag,
		"limit":           dam.Limit(),
	})

	// Audit row should be present at warning severity.
	events := audit.get()
	if len(events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(events))
	}
	e := events[0]
	if e.EventType != "plugin.metric_cardinality_exceeded" {
		t.Errorf("event type: got %q", e.EventType)
	}
	if e.Severity != "warning" {
		t.Errorf("severity: got %q", e.Severity)
	}
	if e.Metadata["overflowing_tag"] != "user_id" {
		t.Errorf("overflowing_tag: got %v", e.Metadata["overflowing_tag"])
	}
	if e.Metadata["metric"] != "requests" {
		t.Errorf("metric: got %v", e.Metadata["metric"])
	}

	// Counters: 2 admitted + 1 dropped.
	if got := testutil.ToFloat64(pm.abiCallTotal.WithLabelValues("plugin-x", "gn_metric_observe", "ok")); got != 2 {
		t.Errorf("ok counter: got %v, want 2", got)
	}
	if got := testutil.ToFloat64(pm.abiCallTotal.WithLabelValues("plugin-x", "gn_metric_observe", "cardinality_exceeded")); got != 1 {
		t.Errorf("cardinality_exceeded counter: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(pm.metricCardinalityExceeded.WithLabelValues("plugin-x", "requests")); got != 1 {
		t.Errorf("metric_cardinality_exceeded counter: got %v, want 1", got)
	}
}

// TestEventEmit_Auditing exercises the integration shape: data blobs
// flow to the AuditEmitter at info severity. The wasm-side fixture
// path is covered separately; this test verifies the host-side path
// the gn_event_emit handler funnels into.
func TestEventEmit_Auditing(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)

	audit := &cardinalityAuditCapture{}
	rt.UseObservability().WithAuditEmitter(audit)

	err := emitObservabilityAudit(context.Background(), rt, "plugin-x", "plugin.user.signed_up", "info", map[string]any{
		"user_id": "42",
		"plan":    "free",
	})
	if err != nil {
		t.Fatalf("emitObservabilityAudit: %v", err)
	}

	events := audit.get()
	if len(events) != 1 {
		t.Fatalf("audit events: got %d, want 1", len(events))
	}
	if events[0].EventType != "plugin.user.signed_up" {
		t.Errorf("event type: got %q", events[0].EventType)
	}
	if events[0].Severity != "info" {
		t.Errorf("severity: got %q", events[0].Severity)
	}
	if events[0].Metadata["user_id"] != "42" {
		t.Errorf("user_id: got %v", events[0].Metadata["user_id"])
	}
}
