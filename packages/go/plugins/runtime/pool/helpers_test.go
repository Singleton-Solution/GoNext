package pool

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// readCounter pulls the current value out of a prometheus.Counter
// via its Collector interface. Prometheus client_golang does not
// expose a Value() method on collectors directly; the canonical way
// to read a counter from a test is to call Write into a dto.Metric.
func readCounter(t *testing.T, c prometheus.Counter) int64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("counter.Write: %v", err)
	}
	if m.Counter == nil || m.Counter.Value == nil {
		return 0
	}
	return int64(*m.Counter.Value)
}

// readCounterVec pulls the current value of a single label
// combination out of a CounterVec.
func readCounterVec(t *testing.T, v *prometheus.CounterVec, labels ...string) int64 {
	t.Helper()
	c, err := v.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("CounterVec.GetMetricWithLabelValues: %v", err)
	}
	return readCounter(t, c)
}
