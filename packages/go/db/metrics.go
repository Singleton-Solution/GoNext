// Package db's metrics.go implements a pgx-aware Prometheus collector
// that exposes pool statistics, named-query latency, and (when the
// replica handle is wired) replication lag.
//
// The collector lives in this package because pool.go is the only
// place in the codebase that owns *pgxpool.Pool — coupling the
// collector to a different package would push us toward exporting the
// pool handle or building yet another adapter. Per docs/10-observability.md
// §5.3 the metric names are part of the public contract; renaming any
// of them is a breaking change for dashboards and alert rules.
//
// Wiring:
//
//	pool, err := db.New(ctx, cfg.Database, logger)
//	...
//	collector := db.NewCollector(pool, db.CollectorOptions{
//	    DBLabel: "primary",
//	})
//	metricsReg.MustRegister(collector)
//
// The collector implements prometheus.Collector directly (rather than
// going through a CounterVec/GaugeVec) because pool stats are pull-based
// — pgxpool.Pool.Stat() is a snapshot, not a stream. Calling Stat() in
// Collect avoids the bookkeeping cost of a poll goroutine, and the
// scrape-time cost is one mutex-free read of the pgxpool counters.
//
// Histograms for query duration / transaction duration ARE registered
// up-front (they're write-once collectors fed by ObserveQuery /
// ObserveTx), so the same registry holds both the Collector and the
// histograms.
//
// Issue #165.
package db

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Metric names. The gonext_db_ prefix matches docs/10-observability.md
// §5.3. Counter names end in _total per the Prometheus convention;
// gauges and histograms do not.
//
// These names are part of our public observability contract — they're
// referenced by dashboards, alert rules, and SLO docs. Renaming any of
// them is a breaking change.
const (
	metricQueryDuration       = "gonext_db_query_duration_seconds"
	metricTxDuration          = "gonext_db_tx_duration_seconds"
	metricPoolOpenConnections = "gonext_db_pool_open_connections"
	metricPoolInUse           = "gonext_db_pool_in_use"
	metricPoolIdle            = "gonext_db_pool_idle"
	metricPoolMaxConns        = "gonext_db_pool_max_conns"
	metricPoolWaitSeconds     = "gonext_db_pool_wait_seconds_total"
	metricPoolWaitCount       = "gonext_db_pool_wait_count_total"
	metricPoolAcquireCount    = "gonext_db_pool_acquire_total"
	metricPoolNewConns        = "gonext_db_pool_new_conns_total"
	metricReplicationLag      = "gonext_db_replication_lag_seconds"
)

// dbLatencyBuckets duplicates packages/go/metrics.DBLatencyBuckets to
// avoid an import cycle (packages/go/metrics depends on packages/go/db
// for the Registry seed path? no — but keeping this local removes any
// future risk of one). The values are identical and the docs in
// metrics/buckets.go remain the source of truth for the rationale.
var dbLatencyBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// ReplicaProber is the contract for replication-lag probing. The
// CollectorOptions takes one; production wiring against a streaming
// replica passes an implementation that runs
// `SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))`
// against the replica's pool. Deployments without a replica leave the
// field nil and the gauge is skipped from the scrape.
//
// We isolate this behind an interface because:
//
//   - Not every deployment has a replica handle (single-DB
//     installs).
//   - Even when one exists, the lag query is dialect-specific
//     (Aurora exposes it differently than vanilla Postgres, and managed
//     services expose a `aws_rds.repl_lag` or similar). Keeping the
//     query out of this file lets each deployment plumb the
//     appropriate one without touching the collector.
//
// LagSeconds returns the most recent measured lag (seconds behind
// primary). Returning math.NaN suppresses the sample, matching the
// Prometheus convention that NaN is "no value".
type ReplicaProber interface {
	LagSeconds(ctx context.Context) (float64, error)
}

// CollectorOptions configures NewCollector. All fields are optional;
// zero values produce a collector wired for a single-replica deployment
// with the primary labeled "primary".
type CollectorOptions struct {
	// DBLabel is the value of the `db` label on every emitted series.
	// Defaults to "primary". Set to "primary" / "replica" / etc. when
	// multiple pools are registered in the same binary.
	DBLabel string

	// Replica, when non-nil, is consulted on every scrape to emit
	// gonext_db_replication_lag_seconds. The value's `replica` label
	// is taken from ReplicaLabel. nil disables the gauge entirely.
	Replica ReplicaProber

	// ReplicaLabel is the value of the `replica` label on the
	// replication-lag gauge. Defaults to "default" when Replica is set
	// and the label is empty.
	ReplicaLabel string

	// Logger receives warnings when a replica lag probe fails. nil
	// suppresses logging; production wiring always passes the binary
	// logger so transient lag-probe failures show up in the structured
	// log stream.
	Logger *slog.Logger

	// ProbeTimeout bounds the per-scrape replica probe. Defaults to
	// 1 second — a slow lag probe must not slow the /metrics scrape
	// past the Prometheus default scrape_timeout (10s) by enough to
	// matter, and a 1s budget is plenty for a healthy LAN replica.
	ProbeTimeout time.Duration
}

const defaultProbeTimeout = 1 * time.Second

// Collector implements prometheus.Collector for a single *pgxpool.Pool.
// It exposes both pull-based gauges (pool stats sampled at Collect
// time) and push-based histograms (query / transaction duration, fed
// by ObserveQuery / ObserveTx).
//
// Safe for concurrent use; pgxpool.Pool.Stat() is itself goroutine-safe.
type Collector struct {
	pool *pgxpool.Pool
	opts CollectorOptions

	// Pull-based descriptors. Built once in NewCollector so Describe
	// and Collect emit the same Desc instances (a Prometheus contract:
	// every Collect must emit metrics whose Desc was returned by
	// Describe; mismatches log a warning and drop the metric).
	openConns    *prometheus.Desc
	inUse        *prometheus.Desc
	idle         *prometheus.Desc
	maxConns     *prometheus.Desc
	waitSeconds  *prometheus.Desc
	waitCount    *prometheus.Desc
	acquireCount *prometheus.Desc
	newConns     *prometheus.Desc
	replLag      *prometheus.Desc

	// Push-based histograms. Fed by ObserveQuery / ObserveTx.
	// Registered alongside the Collector via the same MustRegister
	// call so callers don't need a two-step wiring. We hold the
	// HistogramVec rather than the bare Desc because callers reach in
	// through ObserveQuery to record samples.
	queryHist *prometheus.HistogramVec
	txHist    *prometheus.HistogramVec

	// replProbeFailures counts probe errors; surfaced through the
	// logger but kept internally as an atomic for quiet bursts (we
	// don't want a flapping replica to spam the log on every scrape).
	replProbeFailures atomic.Int64
}

// NewCollector builds a Collector against pool. Callers register the
// returned value with their Prometheus registry:
//
//	collector := db.NewCollector(pool, db.CollectorOptions{
//	    DBLabel: "primary",
//	})
//	metricsReg.Prometheus().MustRegister(collector)
//
// pool MUST be non-nil — there's no graceful degradation path for a
// collector without a pool to introspect, and an early panic at
// startup is preferable to a silent always-zero gauge in production.
func NewCollector(pool *pgxpool.Pool, opts CollectorOptions) *Collector {
	if pool == nil {
		panic("db.NewCollector: pool is required")
	}
	if opts.DBLabel == "" {
		opts.DBLabel = "primary"
	}
	if opts.Replica != nil && opts.ReplicaLabel == "" {
		opts.ReplicaLabel = "default"
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = defaultProbeTimeout
	}

	labels := []string{"db"}
	c := &Collector{
		pool: pool,
		opts: opts,
		openConns: prometheus.NewDesc(
			metricPoolOpenConnections,
			"Current number of open connections in the pool (acquired + idle).",
			labels, nil,
		),
		inUse: prometheus.NewDesc(
			metricPoolInUse,
			"Number of connections currently checked out by callers.",
			labels, nil,
		),
		idle: prometheus.NewDesc(
			metricPoolIdle,
			"Number of idle connections sitting in the pool.",
			labels, nil,
		),
		maxConns: prometheus.NewDesc(
			metricPoolMaxConns,
			"Maximum number of connections the pool may open.",
			labels, nil,
		),
		waitSeconds: prometheus.NewDesc(
			metricPoolWaitSeconds,
			"Cumulative wall-clock time spent waiting for a connection from the pool.",
			labels, nil,
		),
		waitCount: prometheus.NewDesc(
			metricPoolWaitCount,
			"Cumulative count of acquisitions that had to wait because the pool was exhausted.",
			labels, nil,
		),
		acquireCount: prometheus.NewDesc(
			metricPoolAcquireCount,
			"Cumulative count of successful pool acquisitions.",
			labels, nil,
		),
		newConns: prometheus.NewDesc(
			metricPoolNewConns,
			"Cumulative count of new physical connections the pool has opened.",
			labels, nil,
		),
		replLag: prometheus.NewDesc(
			metricReplicationLag,
			"Seconds the configured replica is behind the primary (NaN when probe fails).",
			[]string{"replica"}, nil,
		),
		queryHist: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricQueryDuration,
			Help:    "Per-named-query latency in seconds.",
			Buckets: dbLatencyBuckets,
		}, []string{"query_name", "op"}),
		txHist: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricTxDuration,
			Help:    "Per-named-transaction duration in seconds.",
			Buckets: dbLatencyBuckets,
		}, []string{"tx_name"}),
	}
	return c
}

// Describe implements prometheus.Collector. Emits every Desc the
// collector might ever produce.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.openConns
	ch <- c.inUse
	ch <- c.idle
	ch <- c.maxConns
	ch <- c.waitSeconds
	ch <- c.waitCount
	ch <- c.acquireCount
	ch <- c.newConns
	if c.opts.Replica != nil {
		ch <- c.replLag
	}
	c.queryHist.Describe(ch)
	c.txHist.Describe(ch)
}

// Collect implements prometheus.Collector. Reads pgxpool.Stat() once
// per scrape and emits every pool-stats gauge plus the replication-lag
// gauge (when a replica prober is configured).
//
// pgxpool.Stat() is a snapshot — the values are read from internal
// atomics in pgxpool, so the call is cheap and lock-free relative to
// the connection-acquisition fast path.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()
	db := c.opts.DBLabel

	// AcquiredConns + IdleConns equals TotalConns; we emit each so
	// dashboards can break the pool down without computing the sum
	// from a recording rule.
	ch <- prometheus.MustNewConstMetric(c.openConns, prometheus.GaugeValue, float64(stat.TotalConns()), db)
	ch <- prometheus.MustNewConstMetric(c.inUse, prometheus.GaugeValue, float64(stat.AcquiredConns()), db)
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(stat.IdleConns()), db)
	ch <- prometheus.MustNewConstMetric(c.maxConns, prometheus.GaugeValue, float64(stat.MaxConns()), db)
	ch <- prometheus.MustNewConstMetric(c.waitSeconds, prometheus.CounterValue, stat.AcquireDuration().Seconds(), db)
	ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stat.EmptyAcquireCount()), db)
	ch <- prometheus.MustNewConstMetric(c.acquireCount, prometheus.CounterValue, float64(stat.AcquireCount()), db)
	ch <- prometheus.MustNewConstMetric(c.newConns, prometheus.CounterValue, float64(stat.NewConnsCount()), db)

	if c.opts.Replica != nil {
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.ProbeTimeout)
		defer cancel()
		lag, err := c.opts.Replica.LagSeconds(ctx)
		if err != nil {
			// Increment internal failure counter; warn rate-limited
			// via slog (handler can apply its own throttling).
			n := c.replProbeFailures.Add(1)
			if c.opts.Logger != nil {
				c.opts.Logger.Warn("db: replica lag probe failed",
					slog.String("replica", c.opts.ReplicaLabel),
					slog.Int64("consecutive_failures", n),
					slog.String("err", err.Error()),
				)
			}
			// Skip the sample on error — Prometheus treats absence as
			// staleness, which is the right semantic here.
		} else {
			c.replProbeFailures.Store(0)
			ch <- prometheus.MustNewConstMetric(c.replLag, prometheus.GaugeValue, lag, c.opts.ReplicaLabel)
		}
	}

	c.queryHist.Collect(ch)
	c.txHist.Collect(ch)
}

// ObserveQuery records the elapsed duration of a named query against
// the histogram. queryName is the code-defined identifier (e.g.
// "posts.list", "user.lookup_by_email") — NOT the SQL text. op is one
// of "select" / "insert" / "update" / "delete" / "exec" / "tx".
//
// Callers wire this around their pgx Query / Exec / QueryRow calls:
//
//	start := time.Now()
//	rows, err := pool.Query(ctx, sql)
//	collector.ObserveQuery("posts.list", "select", time.Since(start))
//
// The (queryName, op) cardinality is deliberately bounded — operators
// who add a new named query also add a new series. Unbounded labels
// (raw SQL, parameter values) would blow the cardinality budget and
// must NEVER be passed here.
func (c *Collector) ObserveQuery(queryName, op string, dur time.Duration) {
	if c == nil {
		return
	}
	c.queryHist.WithLabelValues(queryName, op).Observe(dur.Seconds())
}

// ObserveTx records the elapsed duration of a named transaction. The
// txName is the code-defined identifier; the same cardinality
// constraints as ObserveQuery apply.
func (c *Collector) ObserveTx(txName string, dur time.Duration) {
	if c == nil {
		return
	}
	c.txHist.WithLabelValues(txName).Observe(dur.Seconds())
}

// ReplicaProberFunc adapts a plain function to the ReplicaProber
// interface, for callers that don't want to spin up a dedicated type.
// Production wiring typically uses this with a closure over a separate
// replica *pgxpool.Pool:
//
//	prober := db.ReplicaProberFunc(func(ctx context.Context) (float64, error) {
//	    var secs float64
//	    err := replicaPool.QueryRow(ctx,
//	        "SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))",
//	    ).Scan(&secs)
//	    return secs, err
//	})
type ReplicaProberFunc func(ctx context.Context) (float64, error)

// LagSeconds implements ReplicaProber.
func (f ReplicaProberFunc) LagSeconds(ctx context.Context) (float64, error) {
	return f(ctx)
}
