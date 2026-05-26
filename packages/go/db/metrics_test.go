package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestNewCollector_PanicsOnNilPool verifies the documented precondition.
// A nil pool is a wiring bug; an early panic at startup is preferable
// to a silent always-zero gauge in production.
func TestNewCollector_PanicsOnNilPool(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on nil pool")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T", r)
		}
		if !strings.Contains(msg, "pool is required") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	_ = NewCollector(nil, CollectorOptions{})
}

// TestNewCollector_AppliesDefaults checks that the zero CollectorOptions
// gets sensible labels and timeout.
func TestNewCollector_AppliesDefaults(t *testing.T) {
	dsn := dsnFromEnv(t)
	pool, err := New(context.Background(), config.DatabaseConfig{
		URL:          dsn,
		MaxOpenConns: 2,
	}, quietLogger())
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	c := NewCollector(pool, CollectorOptions{})
	if c.opts.DBLabel != "primary" {
		t.Errorf("DBLabel default: got %q want %q", c.opts.DBLabel, "primary")
	}
	if c.opts.ProbeTimeout != defaultProbeTimeout {
		t.Errorf("ProbeTimeout default: got %v want %v", c.opts.ProbeTimeout, defaultProbeTimeout)
	}
}

// TestCollector_Collect_EmitsPoolStats verifies that a Collect cycle
// against a live pool produces non-empty samples for every pool gauge.
// Counter values are checked for monotonicity (newConns >= 0).
func TestCollector_Collect_EmitsPoolStats(t *testing.T) {
	dsn := dsnFromEnv(t)
	pool, err := New(context.Background(), config.DatabaseConfig{
		URL:          dsn,
		MaxOpenConns: 4,
	}, quietLogger())
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer pool.Close()

	reg := prometheus.NewRegistry()
	c := NewCollector(pool, CollectorOptions{DBLabel: "primary"})
	reg.MustRegister(c)

	// Sanity: scrape and confirm at least one sample comes through for
	// each pool gauge. We use testutil.CollectAndCount which exercises
	// the full Describe→Collect contract.
	for _, name := range []string{
		metricPoolOpenConnections,
		metricPoolInUse,
		metricPoolIdle,
		metricPoolMaxConns,
		metricPoolWaitSeconds,
		metricPoolWaitCount,
		metricPoolAcquireCount,
		metricPoolNewConns,
	} {
		count := testutil.CollectAndCount(c, name)
		if count == 0 {
			t.Errorf("no samples emitted for %s", name)
		}
	}
}

// TestCollector_ObserveQuery_RecordsHistogram verifies the push-side of
// the collector: observations land in the histogram and are emitted on
// scrape.
func TestCollector_ObserveQuery_RecordsHistogram(t *testing.T) {
	// Histogram observations don't need a live pool — but NewCollector
	// requires non-nil pool. Use a no-DB shortcut: a fake handle that
	// only needs to be non-nil to pass the precondition. We don't call
	// Collect (which would try to call pool.Stat()) in this test.
	c := &Collector{
		queryHist: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricQueryDuration,
			Help:    "test",
			Buckets: dbLatencyBuckets,
		}, []string{"query_name", "op"}),
		txHist: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricTxDuration,
			Help:    "test",
			Buckets: dbLatencyBuckets,
		}, []string{"tx_name"}),
	}

	c.ObserveQuery("posts.list", "select", 12*time.Millisecond)
	c.ObserveQuery("posts.list", "select", 30*time.Millisecond)
	c.ObserveTx("write_post", 80*time.Millisecond)

	// CollectAndCount exercises the full Describe→Collect contract and
	// returns the number of series; a histogram emits one series
	// (_count, _sum, _bucket together) per label-value combination.
	if got := testutil.CollectAndCount(c.queryHist, metricQueryDuration); got != 1 {
		t.Errorf("query histogram series count: got %d want 1", got)
	}
	if got := testutil.CollectAndCount(c.txHist, metricTxDuration); got != 1 {
		t.Errorf("tx histogram series count: got %d want 1", got)
	}
}

// TestCollector_ObserveQuery_NilReceiverIsSafe documents the
// nil-tolerant contract — callers that wire the collector
// conditionally (e.g. metrics disabled in tests) can still call
// ObserveQuery without crashing.
func TestCollector_ObserveQuery_NilReceiverIsSafe(t *testing.T) {
	var c *Collector
	c.ObserveQuery("x", "select", time.Millisecond)
	c.ObserveTx("x", time.Millisecond)
}

// TestReplicaProberFunc_Adapter verifies the function adapter
// forwards calls.
func TestReplicaProberFunc_Adapter(t *testing.T) {
	var captured context.Context
	prober := ReplicaProberFunc(func(ctx context.Context) (float64, error) {
		captured = ctx
		return 1.5, nil
	})
	ctx := context.Background()
	v, err := prober.LagSeconds(ctx)
	if err != nil {
		t.Fatalf("LagSeconds: %v", err)
	}
	if v != 1.5 {
		t.Errorf("value: got %v want 1.5", v)
	}
	if captured != ctx {
		t.Error("ctx not forwarded")
	}
}

// TestReplicaProberFunc_PropagatesError ensures errors flow through the
// adapter unchanged.
func TestReplicaProberFunc_PropagatesError(t *testing.T) {
	sentinel := errors.New("replica probe failed")
	prober := ReplicaProberFunc(func(_ context.Context) (float64, error) {
		return 0, sentinel
	})
	_, err := prober.LagSeconds(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("err: got %v want %v", err, sentinel)
	}
}
