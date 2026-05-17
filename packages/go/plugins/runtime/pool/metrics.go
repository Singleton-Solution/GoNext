package pool

import "github.com/prometheus/client_golang/prometheus"

// Recycle reason labels for the recycle_total counter. These are
// exhaustive — every code path that closes an instance instead of
// returning it to idle records exactly one of these reasons. The
// fixed set keeps the label cardinality bounded (4 values × N pools)
// regardless of traffic.
const (
	// RecycleReasonTrap means the lease was returned with the trap
	// flag set — typically a guest panic, OOB memory access, or a
	// ctx-cancellation that wazero surfaced as a trap.
	RecycleReasonTrap = "trap"

	// RecycleReasonMaxUses means MaxUsesPerInstance was reached. The
	// instance was healthy but old; the pool replaces it on the
	// theory that long-lived guest state (linear-memory bloat,
	// fragmentation) is a latent risk.
	RecycleReasonMaxUses = "max_uses"

	// RecycleReasonIdle means the reaper closed an instance because
	// it had been idle longer than MaxIdleTime.
	RecycleReasonIdle = "idle"

	// RecycleReasonClose means the pool itself is shutting down.
	// Every remaining instance is closed under this reason so the
	// counter still balances when comparing checkout vs. close.
	RecycleReasonClose = "close"
)

// Metrics is the Prometheus surface for a Pool. One Metrics may be
// shared across multiple pools that all label their series by a
// per-pool plugin name (Metrics does not itself add a "plugin" label
// — callers wrap by passing a labeled CurriedVec or a per-plugin
// Metrics instance via NewMetrics).
//
// All fields are non-nil after NewMetrics; methods on Pool that fire
// metrics call them unconditionally.
type Metrics struct {
	// CheckoutTotal counts every successful Checkout (one observation
	// per call that returns a usable Lease). Failed checkouts (ctx
	// timeout, pool closed) are NOT counted here — they appear in
	// CheckoutErrors so the success rate stays computable as
	// CheckoutTotal / (CheckoutTotal + CheckoutErrors).
	CheckoutTotal prometheus.Counter

	// CheckoutErrors counts Checkout failures (ctx deadline exceeded
	// or pool closed). Useful for alerting when the pool is
	// chronically saturated.
	CheckoutErrors prometheus.Counter

	// CheckoutWaitSeconds is the wait time between calling Checkout
	// and getting a Lease. For pools that are usually idle this is
	// dominated by the spin through the idle slice and lands in the
	// microsecond bucket; under saturation it lands in the seconds
	// bucket and is the primary "is my pool too small?" signal.
	CheckoutWaitSeconds prometheus.Histogram

	// RecycleTotal counts instance disposals, labeled by reason
	// (see RecycleReason* constants above).
	RecycleTotal *prometheus.CounterVec

	// PoolSize is the live count of instances the pool owns
	// (idle + checked-out). Decreases on recycle, increases on lazy
	// create.
	PoolSize prometheus.Gauge

	// InUse is the count of currently-checked-out leases. PoolSize -
	// InUse = idle.
	InUse prometheus.Gauge
}

// MetricsConfig customizes the metric names and registry. Most
// callers leave Namespace and Subsystem empty and accept the default
// "gonext_plugin_pool_" prefix.
type MetricsConfig struct {
	// Registerer is where the new metrics are registered. If nil,
	// the metrics are created but not registered — useful for tests
	// or callers that want to wire registration themselves.
	Registerer prometheus.Registerer

	// Namespace is the Prometheus metric namespace. Default
	// "gonext".
	Namespace string

	// Subsystem is the Prometheus metric subsystem. Default
	// "plugin_pool".
	Subsystem string

	// ConstLabels are added to every metric. Common usage:
	// {"plugin": "<plugin name>"} so a single registry can host
	// multiple pools without label collisions.
	ConstLabels prometheus.Labels

	// WaitBuckets overrides the histogram buckets for
	// CheckoutWaitSeconds. The default is a wide spread from 10 µs
	// to 5 s, which covers both the "hot path uncontended" and the
	// "pool saturated" regimes.
	WaitBuckets []float64
}

// defaultWaitBuckets covers ~10 µs (hot path, idle pool) through ~5 s
// (heavily saturated) with enough resolution to read p50/p95/p99
// usefully at either end.
var defaultWaitBuckets = []float64{
	0.00001, // 10 µs
	0.00005, // 50 µs
	0.0001,  // 100 µs
	0.0005,  // 500 µs
	0.001,   // 1 ms
	0.005,   // 5 ms
	0.01,    // 10 ms
	0.05,    // 50 ms
	0.1,     // 100 ms
	0.5,     // 500 ms
	1.0,     // 1 s
	5.0,     // 5 s
}

// NewMetrics constructs a Metrics. If cfg.Registerer is non-nil, the
// metrics are registered against it; an already-registered duplicate
// returns the existing collector rather than panicking.
//
// Returns nil if the registry returns an unrecoverable error (e.g. a
// non-AlreadyRegisteredError from MustRegister). In practice this
// can only happen on programming errors at startup, so callers can
// safely treat a non-nil return as the only success path.
func NewMetrics(cfg MetricsConfig) *Metrics {
	ns := cfg.Namespace
	if ns == "" {
		ns = "gonext"
	}
	sub := cfg.Subsystem
	if sub == "" {
		sub = "plugin_pool"
	}
	buckets := cfg.WaitBuckets
	if len(buckets) == 0 {
		buckets = defaultWaitBuckets
	}

	m := &Metrics{
		CheckoutTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace:   ns,
			Subsystem:   sub,
			Name:        "checkout_total",
			Help:        "Total successful pool checkouts.",
			ConstLabels: cfg.ConstLabels,
		}),
		CheckoutErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace:   ns,
			Subsystem:   sub,
			Name:        "checkout_errors_total",
			Help:        "Total failed pool checkouts (timeout or pool closed).",
			ConstLabels: cfg.ConstLabels,
		}),
		CheckoutWaitSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace:   ns,
			Subsystem:   sub,
			Name:        "checkout_wait_seconds",
			Help:        "Wait time from Checkout call to Lease acquisition.",
			Buckets:     buckets,
			ConstLabels: cfg.ConstLabels,
		}),
		RecycleTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   ns,
			Subsystem:   sub,
			Name:        "recycle_total",
			Help:        "Total pool instance recycles, by reason.",
			ConstLabels: cfg.ConstLabels,
		}, []string{"reason"}),
		PoolSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   ns,
			Subsystem:   sub,
			Name:        "pool_size",
			Help:        "Current number of instances owned by the pool (idle + checked out).",
			ConstLabels: cfg.ConstLabels,
		}),
		InUse: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   ns,
			Subsystem:   sub,
			Name:        "in_use",
			Help:        "Current number of checked-out leases.",
			ConstLabels: cfg.ConstLabels,
		}),
	}

	if cfg.Registerer != nil {
		// Each collector is registered separately so a partial
		// AlreadyRegisteredError on one metric doesn't block the
		// others. registerOrReuse handles the duplicate case so a
		// pool created against an already-seeded registry (e.g.
		// after a hot reload) keeps working with the existing
		// series.
		m.CheckoutTotal = registerOrReuseCounter(cfg.Registerer, m.CheckoutTotal)
		m.CheckoutErrors = registerOrReuseCounter(cfg.Registerer, m.CheckoutErrors)
		m.CheckoutWaitSeconds = registerOrReuseHistogram(cfg.Registerer, m.CheckoutWaitSeconds)
		m.RecycleTotal = registerOrReuseCounterVec(cfg.Registerer, m.RecycleTotal)
		m.PoolSize = registerOrReuseGauge(cfg.Registerer, m.PoolSize)
		m.InUse = registerOrReuseGauge(cfg.Registerer, m.InUse)
	}

	return m
}

// registerOrReuseCounter registers c with reg. If reg reports the
// metric is already registered, returns the pre-existing collector
// so the caller can keep writing to a single underlying series.
//
// Any other error is silently swallowed: the unregistered collector
// is returned and writes to it become no-ops. We prefer this over
// panicking because metrics are observability, not correctness — a
// broken registry should not crash the binary.
func registerOrReuseCounter(reg prometheus.Registerer, c prometheus.Counter) prometheus.Counter {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if asErr(err, &are) {
			if existing, ok := are.ExistingCollector.(prometheus.Counter); ok {
				return existing
			}
		}
	}
	return c
}

func registerOrReuseHistogram(reg prometheus.Registerer, h prometheus.Histogram) prometheus.Histogram {
	if err := reg.Register(h); err != nil {
		var are prometheus.AlreadyRegisteredError
		if asErr(err, &are) {
			if existing, ok := are.ExistingCollector.(prometheus.Histogram); ok {
				return existing
			}
		}
	}
	return h
}

func registerOrReuseCounterVec(reg prometheus.Registerer, v *prometheus.CounterVec) *prometheus.CounterVec {
	if err := reg.Register(v); err != nil {
		var are prometheus.AlreadyRegisteredError
		if asErr(err, &are) {
			if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
	}
	return v
}

func registerOrReuseGauge(reg prometheus.Registerer, g prometheus.Gauge) prometheus.Gauge {
	if err := reg.Register(g); err != nil {
		var are prometheus.AlreadyRegisteredError
		if asErr(err, &are) {
			if existing, ok := are.ExistingCollector.(prometheus.Gauge); ok {
				return existing
			}
		}
	}
	return g
}

// asErr is a tiny errors.As wrapper kept local so this file does not
// have to import "errors" — the rest of the file is metrics-only and
// the import would feel out of place.
func asErr(err error, target any) bool { return errorsAs(err, target) }
