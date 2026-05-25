package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newTestMetrics returns a fresh PluginMetrics registered against an
// isolated Prometheus registry so each test sees a clean state.
func newTestMetrics(t *testing.T, limit int) (*PluginMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	pm := NewPluginMetricsWithLimit(reg, limit)
	return pm, reg
}

func TestPluginMetrics_RegisterSlug_HonoursLimit(t *testing.T) {
	pm, _ := newTestMetrics(t, 2)

	if err := pm.RegisterSlug("plugin-a"); err != nil {
		t.Fatalf("RegisterSlug(a): %v", err)
	}
	if err := pm.RegisterSlug("plugin-b"); err != nil {
		t.Fatalf("RegisterSlug(b): %v", err)
	}
	err := pm.RegisterSlug("plugin-c")
	if err == nil {
		t.Fatal("RegisterSlug(c): expected error at limit, got nil")
	}
	var limitErr *ErrPluginSlugLimit
	if !errors.As(err, &limitErr) {
		t.Errorf("RegisterSlug(c): err type = %T, want *ErrPluginSlugLimit", err)
	}
	if !strings.Contains(err.Error(), "plugin-c") {
		t.Errorf("error message should name the failing slug, got: %v", err)
	}
}

func TestPluginMetrics_RegisterSlug_Idempotent(t *testing.T) {
	pm, _ := newTestMetrics(t, 1)

	if err := pm.RegisterSlug("plugin-a"); err != nil {
		t.Fatalf("RegisterSlug 1: %v", err)
	}
	if err := pm.RegisterSlug("plugin-a"); err != nil {
		t.Errorf("RegisterSlug 2 (same slug): %v", err)
	}
	if got := pm.SlugCount(); got != 1 {
		t.Errorf("SlugCount: got %d, want 1", got)
	}
}

func TestPluginMetrics_RegisterSlug_EmptyRejected(t *testing.T) {
	pm, _ := newTestMetrics(t, 10)
	if err := pm.RegisterSlug(""); err == nil {
		t.Error("RegisterSlug(\"\"): expected error, got nil")
	}
}

func TestPluginMetrics_Unregister(t *testing.T) {
	pm, _ := newTestMetrics(t, 1)
	_ = pm.RegisterSlug("plugin-a")
	pm.UnregisterSlug("plugin-a")
	if got := pm.SlugCount(); got != 0 {
		t.Errorf("SlugCount after unregister: got %d, want 0", got)
	}
	// Unregistered slug routes to _overflow.
	pm.IncABICall("plugin-a", "gn_log", "ok")
	got := testutil.ToFloat64(pm.abiCallTotal.WithLabelValues("_overflow", "gn_log", "cardinality_exceeded"))
	if got != 1 {
		t.Errorf("after unregister, abi_call should route to _overflow, got %v", got)
	}
}

func TestPluginMetrics_IncABICall_RoutesByAdmission(t *testing.T) {
	pm, _ := newTestMetrics(t, 10)
	_ = pm.RegisterSlug("admitted")

	pm.IncABICall("admitted", "gn_log", "ok")
	pm.IncABICall("admitted", "gn_log", "ok")
	pm.IncABICall("rogue", "gn_log", "ok")

	if got := testutil.ToFloat64(pm.abiCallTotal.WithLabelValues("admitted", "gn_log", "ok")); got != 2 {
		t.Errorf("admitted abi_call: got %v, want 2", got)
	}
	if got := testutil.ToFloat64(pm.abiCallTotal.WithLabelValues("_overflow", "gn_log", "cardinality_exceeded")); got != 1 {
		t.Errorf("overflow abi_call: got %v, want 1", got)
	}
}

func TestPluginMetrics_IncLifecycle_NormalisesEvent(t *testing.T) {
	pm, _ := newTestMetrics(t, 10)
	_ = pm.RegisterSlug("plugin-a")

	pm.IncLifecycle("plugin-a", "create")
	pm.IncLifecycle("plugin-a", "destroy")
	pm.IncLifecycle("plugin-a", "trap")
	pm.IncLifecycle("plugin-a", "weirdo") // folded to "unknown"

	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("plugin-a", "create")); got != 1 {
		t.Errorf("create: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("plugin-a", "destroy")); got != 1 {
		t.Errorf("destroy: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("plugin-a", "trap")); got != 1 {
		t.Errorf("trap: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("plugin-a", "unknown")); got != 1 {
		t.Errorf("unknown (folded): got %v, want 1", got)
	}
}

func TestPluginMetrics_IncFuel(t *testing.T) {
	pm, _ := newTestMetrics(t, 10)
	_ = pm.RegisterSlug("plugin-a")

	pm.IncFuel("plugin-a", 25.5)
	pm.IncFuel("plugin-a", 14.5)
	pm.IncFuel("plugin-a", 0) // ignored
	pm.IncFuel("rogue", 100)  // ignored — unregistered

	if got := testutil.ToFloat64(pm.fuelConsumed.WithLabelValues("plugin-a")); got != 40 {
		t.Errorf("fuel admitted: got %v, want 40", got)
	}
}

func TestPluginMetrics_IncTimeout(t *testing.T) {
	pm, _ := newTestMetrics(t, 10)
	_ = pm.RegisterSlug("plugin-a")

	pm.IncTimeout("plugin-a", "gn_log")
	pm.IncTimeout("plugin-a", "") // → "_call"

	if got := testutil.ToFloat64(pm.timeoutTotal.WithLabelValues("plugin-a", "gn_log")); got != 1 {
		t.Errorf("timeout(gn_log): got %v, want 1", got)
	}
	if got := testutil.ToFloat64(pm.timeoutTotal.WithLabelValues("plugin-a", "_call")); got != 1 {
		t.Errorf("timeout(empty→_call): got %v, want 1", got)
	}
}

func TestRuntime_RegisterPluginSlug_RejectsAtCap(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)
	pm, _ := newTestMetrics(t, 2)
	rt.UseObservability().WithPluginMetrics(pm)

	if err := rt.RegisterPluginSlug("a"); err != nil {
		t.Fatalf("RegisterPluginSlug(a): %v", err)
	}
	if err := rt.RegisterPluginSlug("b"); err != nil {
		t.Fatalf("RegisterPluginSlug(b): %v", err)
	}
	err := rt.RegisterPluginSlug("c")
	if err == nil {
		t.Fatal("expected ErrPluginSlugLimit at cap, got nil")
	}
	var limitErr *ErrPluginSlugLimit
	if !errors.As(err, &limitErr) {
		t.Errorf("err type = %T, want *ErrPluginSlugLimit", err)
	}
	// Lifecycle counter should reflect the two successful create
	// events but NOT the rejected one.
	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("a", "create")); got != 1 {
		t.Errorf("a.create: got %v, want 1", got)
	}
	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("c", "create")); got != 0 {
		t.Errorf("c.create: got %v, want 0 (registration was rejected)", got)
	}
}

func TestRuntime_UnregisterPluginSlug_BumpsDestroy(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)
	pm, _ := newTestMetrics(t, 10)
	rt.UseObservability().WithPluginMetrics(pm)

	_ = rt.RegisterPluginSlug("plugin-a")
	rt.UnregisterPluginSlug("plugin-a")

	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("plugin-a", "destroy")); got != 1 {
		t.Errorf("destroy counter: got %v, want 1", got)
	}
}

func TestRuntime_ObservePluginTrap_BumpsTrapCounter(t *testing.T) {
	rt, _ := newTestRuntime(t)
	defer observabilityRegistry.Delete(rt)
	pm, _ := newTestMetrics(t, 10)
	rt.UseObservability().WithPluginMetrics(pm)
	_ = rt.RegisterPluginSlug("plugin-a")

	rt.ObservePluginTrap(t.Context(), TrapEvent{
		Slug:   "plugin-a",
		Reason: "boom",
	})

	if got := testutil.ToFloat64(pm.instanceLifecycle.WithLabelValues("plugin-a", "trap")); got != 1 {
		t.Errorf("trap counter: got %v, want 1", got)
	}
}
