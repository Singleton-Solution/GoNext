package metrics

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// quietLogger returns a slog.Logger that discards everything.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestNewRegistry_PreRegistersRuntimeCollectors(t *testing.T) {
	r := NewRegistry()

	mf, err := r.Prometheus().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	want := map[string]bool{
		"go_goroutines":                 false,
		"process_resident_memory_bytes": false,
	}
	for _, m := range mf {
		if _, ok := want[m.GetName()]; ok {
			want[m.GetName()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected default collector %q to be registered", name)
		}
	}
}

func TestNewRegistryFrom_NilFallsBackToFresh(t *testing.T) {
	r := NewRegistryFrom(nil)
	if r == nil || r.reg == nil {
		t.Fatal("NewRegistryFrom(nil) should return a non-nil Registry")
	}
}

func TestRegistry_NewCounterRegistersAndCollects(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("gonext_test_counter_total", "test counter", "label")
	c.WithLabelValues("a").Inc()
	c.WithLabelValues("a").Add(2)
	c.WithLabelValues("b").Inc()

	if got := testutil.ToFloat64(c.WithLabelValues("a")); got != 3 {
		t.Errorf("counter a: got %v, want 3", got)
	}
	if got := testutil.ToFloat64(c.WithLabelValues("b")); got != 1 {
		t.Errorf("counter b: got %v, want 1", got)
	}
}

func TestRegistry_NewHistogramUsesProvidedBuckets(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram(
		"gonext_test_histogram_seconds",
		"test histogram",
		[]float64{0.1, 1, 10},
		"op",
	)
	h.WithLabelValues("read").Observe(0.05)
	h.WithLabelValues("read").Observe(0.5)
	h.WithLabelValues("read").Observe(5)

	mf, err := r.Prometheus().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	var found *string
	for _, m := range mf {
		if m.GetName() == "gonext_test_histogram_seconds" {
			n := m.GetName()
			found = &n
			if len(m.GetMetric()) == 0 {
				t.Fatal("histogram has no samples")
			}
			hist := m.GetMetric()[0].GetHistogram()
			if hist.GetSampleCount() != 3 {
				t.Errorf("sample count: got %d, want 3", hist.GetSampleCount())
			}
			if got := len(hist.GetBucket()); got != 3 {
				t.Errorf("bucket count: got %d, want 3", got)
			}
		}
	}
	if found == nil {
		t.Error("histogram was not gathered")
	}
}

func TestRegistry_NewHistogramNilBucketsUsesDefaults(t *testing.T) {
	r := NewRegistry()
	// Should not panic with nil/empty buckets.
	h := r.NewHistogram("gonext_test_default_buckets_seconds", "test", nil, "op")
	h.WithLabelValues("x").Observe(0.5)
}

func TestRegistry_NewGaugeRegistersAndCollects(t *testing.T) {
	r := NewRegistry()
	g := r.NewGauge("gonext_test_gauge", "test gauge", "label")
	g.WithLabelValues("a").Set(42)

	if got := testutil.ToFloat64(g.WithLabelValues("a")); got != 42 {
		t.Errorf("gauge: got %v, want 42", got)
	}
}

func TestRegistry_DuplicateRegistration_Panics(t *testing.T) {
	r := NewRegistry()
	r.NewCounter("gonext_test_dup_total", "first", "l")

	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	r.NewCounter("gonext_test_dup_total", "second", "l")
}

func TestRegistry_RegisterReturnsErrorOnDup(t *testing.T) {
	r := NewRegistry()
	c1 := prometheus.NewCounter(prometheus.CounterOpts{Name: "gonext_test_register_total", Help: "x"})
	if err := r.Register(c1); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	c2 := prometheus.NewCounter(prometheus.CounterOpts{Name: "gonext_test_register_total", Help: "x"})
	err := r.Register(c2)
	if err == nil {
		t.Fatal("expected duplicate-registration error")
	}
	if !strings.Contains(err.Error(), "metrics.Registry.Register") {
		t.Errorf("error should be wrapped, got: %v", err)
	}
}

func TestMustBoundedLabels_LogsUnbounded(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	MustBoundedLabels(
		[]string{"route", "method", "user_id"},
		map[string]int{
			"route":  150,
			"method": 7,
		},
		logger,
	)

	out := buf.String()
	if !strings.Contains(out, "no documented cardinality bound") {
		t.Errorf("expected warn log about unbounded labels, got: %s", out)
	}
	if !strings.Contains(out, "user_id") {
		t.Errorf("expected warn to mention user_id, got: %s", out)
	}
	// Bounded labels should not trigger the warning.
	if strings.Contains(out, "route") && strings.Contains(out, "method") &&
		strings.Contains(strings.SplitN(out, "labels=", 2)[1], "route") {
		// route appears in the labels= field only if listed as unbounded.
		// Simple check: ensure the listed labels are exactly user_id.
		if !strings.Contains(out, "labels=user_id") {
			t.Errorf("unbounded labels list should be exactly 'user_id', got: %s", out)
		}
	}
}

func TestMustBoundedLabels_LogsZeroBound(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	MustBoundedLabels(
		[]string{"route"},
		map[string]int{"route": 0},
		logger,
	)

	if !strings.Contains(buf.String(), "zero or negative") {
		t.Errorf("expected zero-bound warning, got: %s", buf.String())
	}
}

func TestMustBoundedLabels_AllBounded_NoWarn(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	MustBoundedLabels(
		[]string{"route", "method"},
		map[string]int{"route": 150, "method": 7},
		logger,
	)

	if buf.Len() != 0 {
		t.Errorf("expected no log output, got: %s", buf.String())
	}
}

func TestMustBoundedLabels_NilLoggerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on nil logger")
		}
	}()
	MustBoundedLabels([]string{"x"}, map[string]int{"x": 1}, nil)
}

func TestBuckets_AreAscending(t *testing.T) {
	cases := []struct {
		name string
		b    []float64
	}{
		{"HTTPLatencyBuckets", HTTPLatencyBuckets},
		{"DBLatencyBuckets", DBLatencyBuckets},
		{"BytesBuckets", BytesBuckets},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.b) == 0 {
				t.Fatal("bucket set is empty")
			}
			for i := 1; i < len(c.b); i++ {
				if c.b[i] <= c.b[i-1] {
					t.Errorf("bucket[%d]=%v not greater than bucket[%d]=%v",
						i, c.b[i], i-1, c.b[i-1])
				}
			}
		})
	}
}
