package asynq

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
)

// Metric names. The gonext_jobs_ prefix follows the project's metric
// naming convention (docs/10-observability.md §5.3). Counter names end in
// _total per the Prometheus convention; gauge names do not.
//
// These names are part of our public observability contract — they're
// referenced by dashboards, alert rules, and SLO docs. Renaming any of
// them is a breaking change.
const (
	metricProcessed = "gonext_jobs_processed_total"
	metricFailed    = "gonext_jobs_failed_total"
	metricInflight  = "gonext_jobs_inflight"
	metricUnknown   = "gonext_jobs_unknown_total"

	// Inspector-driven series (issue #172). These are sampled from
	// Asynq's Inspector API rather than from the in-process handler
	// middleware, so they reflect cluster-wide state (the queue lives
	// in Redis and is shared across every worker replica).
	metricQueueDepth    = "gonext_jobs_queue_depth"
	metricQueueActive   = "gonext_jobs_queue_active"
	metricQueueLagSecs  = "gonext_jobs_queue_lag_seconds"
	metricRetries       = "gonext_jobs_retries"
	metricDLQSize       = "gonext_jobs_dlq_size"
	metricQueuePaused   = "gonext_jobs_queue_paused"
	metricInspectorFail = "gonext_jobs_inspector_failures_total"
)

// metrics bundles the four Prometheus collectors emitted by the chassis.
// One instance per server; collectors are owned by the supplied
// Registerer and stay alive for its lifetime.
//
// All series are labeled by queue. We deliberately do NOT add a "task
// type" label — task types are an unbounded set (every handler defines
// its own string) and would blow the cardinality budget. Per-task-type
// breakdowns belong in tracing, not Prometheus.
type metrics struct {
	processed *prometheus.CounterVec
	failed    *prometheus.CounterVec
	inflight  *prometheus.GaugeVec
	unknown   *prometheus.CounterVec
}

// newMetrics registers the four collectors against reg. Passing a nil
// Registerer is supported (returns a metrics struct whose methods are
// no-ops) so unit tests that don't care about Prometheus don't have to
// plumb a registry through.
//
// Registration uses prometheus.NewCounterVec + reg.MustRegister rather
// than going through packages/go/metrics.Registry's typed helpers — we
// want this package to be usable by tests that build their own raw
// prometheus.Registry, and we don't want to drag the whole metrics
// package in for two lines of collector wiring.
func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricProcessed,
			Help: "Total number of tasks successfully processed, by queue.",
		}, []string{"queue"}),
		failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricFailed,
			Help: "Total number of tasks whose handler returned an error, by queue.",
		}, []string{"queue"}),
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricInflight,
			Help: "Number of tasks currently executing on this server, by queue.",
		}, []string{"queue"}),
		unknown: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricUnknown,
			Help: "Total number of tasks routed to the NotFound handler (unknown task type), by queue.",
		}, []string{"queue"}),
	}
	if reg == nil {
		return m
	}
	reg.MustRegister(m.processed, m.failed, m.inflight, m.unknown)
	return m
}

// observeStart bumps the inflight gauge as a handler begins. Always
// paired with observeFinish in a defer. The queue label is one of the
// seven canonical queue names; we don't sanity-check that here because
// the value comes from Asynq itself.
func (m *metrics) observeStart(queue string) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(queue).Inc()
}

// observeFinish records a handler outcome. err == nil increments
// processed; non-nil increments failed. Always decrements inflight,
// regardless of outcome — the symmetric pairing with observeStart is
// what keeps the gauge accurate even under panic recovery.
func (m *metrics) observeFinish(queue string, err error) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(queue).Dec()
	if err != nil {
		m.failed.WithLabelValues(queue).Inc()
		return
	}
	m.processed.WithLabelValues(queue).Inc()
}

// observeUnknown increments the unknown counter without changing
// processed/failed. This separates "we ran a real handler and it
// failed" from "we got a task we don't know how to run", which lets
// operators detect deploy/code skew without it showing up as a generic
// error spike.
//
// Asynq still counts the NACK as a failure internally (the task
// retries, then archives), so unknown is additive context rather than
// a substitute for the failed metric.
func (m *metrics) observeUnknown(queue string) {
	if m == nil {
		return
	}
	m.unknown.WithLabelValues(queue).Inc()
}

// QueueInspector is the subset of *asynq.Inspector that the
// InspectorCollector needs. Defined as an interface so tests can
// supply a fake without standing up Redis. Production wiring passes
// *asynq.Inspector directly — its method signatures satisfy the
// interface.
//
// The two methods cover the full state set:
//
//   - GetQueueInfo returns Pending / Active / Retry / Archived /
//     Latency / Paused for one queue. We sample this for each
//     configured queue on every scrape.
//
//   - Queues returns the full set of queue names known to the cluster.
//     We use this to default the Collector's queue list when the
//     caller doesn't pass one — it's the right default for an
//     operator who just wants "everything".
type QueueInspector interface {
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
	Queues() ([]string, error)
	Close() error
}

// InspectorCollectorOptions configures NewInspectorCollector. All
// fields are optional; the zero value samples every queue Asynq knows
// about and emits one warning per inspector failure.
type InspectorCollectorOptions struct {
	// Queues, when non-empty, restricts sampling to the named queues.
	// Empty means "ask Inspector.Queues() once per Collect and sample
	// all of them" — appropriate for a single-tenant deployment where
	// every queue should be on the dashboard. Production wiring with
	// known queue names should pass them here explicitly so a typo'd
	// queue name in a publisher doesn't silently inflate the
	// cardinality.
	Queues []string

	// Logger receives Warn lines on Inspector failures. nil suppresses
	// logging; production wiring always passes the binary logger.
	Logger *slog.Logger

	// ProbeTimeout bounds the per-scrape Inspector calls. Defaults to
	// 2 seconds — Asynq's Inspector talks to Redis, and a stalled
	// Redis must not block the /metrics scrape past Prometheus's
	// default scrape_timeout. Two seconds gives plenty of headroom on
	// a healthy LAN while still surfacing problems quickly.
	ProbeTimeout time.Duration
}

const defaultInspectorProbeTimeout = 2 * time.Second

// InspectorCollector implements prometheus.Collector by sampling
// Asynq's Inspector API on every scrape. Exposes queue depth (Pending),
// active count, processing lag (Latency of the oldest pending task),
// retries gauge, and dead-letter (Archived) size — completing the
// observability story that the handler-side metrics start.
//
// One Collector per worker process suffices; the Inspector reads from
// Redis, which is shared across replicas, so each replica's scrape
// gets the same cluster-wide view. We don't deduplicate across
// replicas — Prometheus will scrape each replica's /metrics and the
// recording rules sum/avg as needed.
//
// Safe for concurrent use; the underlying Inspector is goroutine-safe.
type InspectorCollector struct {
	inspector QueueInspector
	opts      InspectorCollectorOptions

	depth        *prometheus.Desc
	active       *prometheus.Desc
	lagSecs      *prometheus.Desc
	retries      *prometheus.Desc
	dlqSize      *prometheus.Desc
	paused       *prometheus.Desc
	probeFails   *prometheus.Desc
	failureCount atomic.Int64
}

// NewInspectorCollector builds an InspectorCollector against
// inspector. The inspector must be non-nil — a nil inspector is a
// wiring bug and an early panic is preferable to a silent always-zero
// dashboard in production.
//
// Wiring (worker main.go):
//
//	inspector := asynq.NewInspector(redisOpt)
//	collector := jobsasynq.NewInspectorCollector(inspector, jobsasynq.InspectorCollectorOptions{
//	    Queues: []string{
//	        jobsasynq.QueueCritical, jobsasynq.QueueWebhook, ...
//	    },
//	    Logger: logger,
//	})
//	metricsReg.MustRegister(collector)
//	orch.MustRegister(logger, "asynq.inspector", shutdown.CloserFromIO(inspector))
func NewInspectorCollector(inspector QueueInspector, opts InspectorCollectorOptions) *InspectorCollector {
	if inspector == nil {
		panic("jobs/asynq.NewInspectorCollector: inspector is required")
	}
	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = defaultInspectorProbeTimeout
	}

	labels := []string{"queue"}
	return &InspectorCollector{
		inspector: inspector,
		opts:      opts,
		depth: prometheus.NewDesc(
			metricQueueDepth,
			"Number of pending tasks in the queue (cluster-wide).",
			labels, nil,
		),
		active: prometheus.NewDesc(
			metricQueueActive,
			"Number of tasks currently being processed across all workers, by queue.",
			labels, nil,
		),
		lagSecs: prometheus.NewDesc(
			metricQueueLagSecs,
			"Age in seconds of the oldest pending task in the queue (processing lag).",
			labels, nil,
		),
		retries: prometheus.NewDesc(
			metricRetries,
			"Number of tasks scheduled for retry after a handler failure, by queue.",
			labels, nil,
		),
		dlqSize: prometheus.NewDesc(
			metricDLQSize,
			"Number of tasks in the dead-letter queue (archived after exhausting retries), by queue.",
			labels, nil,
		),
		paused: prometheus.NewDesc(
			metricQueuePaused,
			"1 when the queue is paused (operators stopped dispatch); 0 otherwise.",
			labels, nil,
		),
		probeFails: prometheus.NewDesc(
			metricInspectorFail,
			"Cumulative count of Inspector probe failures during /metrics scrapes.",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *InspectorCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.depth
	ch <- c.active
	ch <- c.lagSecs
	ch <- c.retries
	ch <- c.dlqSize
	ch <- c.paused
	ch <- c.probeFails
}

// Collect implements prometheus.Collector. For each configured queue
// (or, if none configured, the full set returned by Inspector.Queues),
// issues one GetQueueInfo call and emits all six per-queue gauges.
//
// Per-queue probe failures are logged and counted, but never propagate
// — a transient Redis blip should not cause Prometheus to lose the
// rest of the scrape. The probe-failure counter is itself a Prometheus
// gauge so operators can alert on it.
func (c *InspectorCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.ProbeTimeout)
	defer cancel()

	queues := c.opts.Queues
	if len(queues) == 0 {
		discovered, err := c.inspector.Queues()
		if err != nil {
			c.recordProbeFailure("queues_discovery", err)
		} else {
			queues = discovered
		}
	}

	for _, q := range queues {
		// Respect the scrape-level timeout: if ctx is done, stop
		// emitting rather than blocking on more Redis round-trips.
		if err := ctx.Err(); err != nil {
			c.recordProbeFailure("context_deadline_exceeded", err)
			break
		}
		info, err := c.inspector.GetQueueInfo(q)
		if err != nil {
			c.recordProbeFailure(q, err)
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.depth, prometheus.GaugeValue, float64(info.Pending), q)
		ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(info.Active), q)
		ch <- prometheus.MustNewConstMetric(c.lagSecs, prometheus.GaugeValue, info.Latency.Seconds(), q)
		ch <- prometheus.MustNewConstMetric(c.retries, prometheus.GaugeValue, float64(info.Retry), q)
		ch <- prometheus.MustNewConstMetric(c.dlqSize, prometheus.GaugeValue, float64(info.Archived), q)
		var paused float64
		if info.Paused {
			paused = 1
		}
		ch <- prometheus.MustNewConstMetric(c.paused, prometheus.GaugeValue, paused, q)
	}

	ch <- prometheus.MustNewConstMetric(c.probeFails, prometheus.CounterValue, float64(c.failureCount.Load()))
}

// recordProbeFailure logs and counts an Inspector failure. We
// emit a single Warn line per failure — operators tune Prometheus's
// scrape interval and the Inspector probe runs once per scrape, so the
// log rate is bounded by the scrape cadence rather than by the in-flight
// task volume.
func (c *InspectorCollector) recordProbeFailure(queue string, err error) {
	c.failureCount.Add(1)
	if c.opts.Logger != nil {
		c.opts.Logger.Warn("jobs/asynq: inspector probe failed; skipping queue sample",
			slog.String("queue", queue),
			slog.String("err", err.Error()),
		)
	}
}
