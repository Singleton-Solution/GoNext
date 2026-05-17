package asynq

import (
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Queue names. Kept as exported constants so callers (task registries,
// dashboards, runbooks) reference one source of truth. Changing a name
// here is a breaking change — every enqueue call in the codebase, every
// Grafana dashboard, and every operator's bookmarked URL would break.
//
// Values match docs/12-jobs-cron.md §2 once issue #256 reconciles the
// doc's transitional names (`cache`, `low`, `plugins`) to the final seven.
const (
	QueueCritical  = "critical"
	QueueWebhook   = "webhook"
	QueueEmail     = "email"
	QueueMedia     = "media"
	QueueMigration = "migration"
	QueuePlugin    = "plugin"
	QueueDefault   = "default"
)

// Weights for each queue. Asynq schedules tasks with probability
// proportional to weight, so these numbers literally control "how often
// does `critical` run vs `default` when both have work". Sum is 28; that
// matters only for back-of-envelope math, Asynq normalizes internally.
//
// The 10/5/5/3/2/2/1 split came out of the issue #256 design review:
// `critical` dominates user-blocking flows; `webhook` and `email` are
// peers on the next tier (both are operator/integration-facing, neither
// should starve the other); `media` is CPU-bound and benefits from being
// reliably scheduled but not at the expense of latency-sensitive work;
// `migration` and `plugin` are batch / untrusted, intentionally lower;
// `default` is the catch-all and the implicit fallback for unrouted
// tasks (so it must always run a little).
const (
	weightCritical  = 10
	weightWebhook   = 5
	weightEmail     = 5
	weightMedia     = 3
	weightMigration = 2
	weightPlugin    = 2
	weightDefault   = 1
)

// Defaults. Picked deliberately:
//
//   - defaultConcurrency = 32: matches the sum of the per-queue worker
//     budgets in the project's capacity plan (4+8+8+8+2+2+0). The "0" is
//     `default`'s — it borrows from the shared pool. Operators tune via
//     env var without touching code.
//
//   - defaultShutdownTimeout = 3 * time.Minute: must be < the worker
//     binary's overall drain budget (240s) by a margin big enough for the
//     other registered closers (Redis, metrics, audit) to run after the
//     queue server stops. 3m leaves ~60s of headroom which is plenty.
//
//   - defaultHealthCheckInterval = 15 * time.Second: same as Asynq's
//     built-in default but we name it explicitly so /readyz behavior is
//     reproducible.
const (
	defaultConcurrency         = 32
	defaultShutdownTimeout     = 3 * time.Minute
	defaultHealthCheckInterval = 15 * time.Second
)

// DefaultQueues returns the canonical 7-queue weighted topology. Callers
// constructing a Config without Queues set get this map applied at
// validate time; tests assert on the contents so accidental drift fails
// CI.
//
// The function returns a fresh copy so the caller can mutate (e.g. boost
// `media` on a CPU-rich replica) without contaminating other Configs in
// the same process.
func DefaultQueues() map[string]int {
	return map[string]int{
		QueueCritical:  weightCritical,
		QueueWebhook:   weightWebhook,
		QueueEmail:     weightEmail,
		QueueMedia:     weightMedia,
		QueueMigration: weightMigration,
		QueuePlugin:    weightPlugin,
		QueueDefault:   weightDefault,
	}
}

// Config configures the chassis. Every field has a defaulting policy
// applied by validate() so an empty Config is valid and produces the
// production-canonical server. Tests typically only set Logger.
type Config struct {
	// Queues maps queue name → weight. Empty/nil triggers DefaultQueues.
	// Weights must be positive; zero or negative entries are rejected
	// (Asynq itself silently drops them, but we want the boot-time loud).
	Queues map[string]int

	// Concurrency is the total worker-goroutine budget across all queues.
	// Asynq treats this as a single shared pool, so the weighted queues
	// share it proportionally. Defaults to defaultConcurrency.
	Concurrency int

	// ShutdownTimeout bounds how long Asynq waits for in-flight handlers
	// during graceful shutdown before NACK-ing remaining tasks back to
	// Redis. Defaults to defaultShutdownTimeout. Must be shorter than the
	// containing shutdown orchestrator's budget; we don't validate that
	// here because it depends on caller context.
	ShutdownTimeout time.Duration

	// HealthCheckInterval is how often Asynq pings Redis. The result feeds
	// Healthy() and the /readyz check. Defaults to
	// defaultHealthCheckInterval.
	HealthCheckInterval time.Duration

	// StrictPriority makes Asynq drain higher-priority queues completely
	// before touching lower-priority queues. Off by default — we want
	// weighted scheduling (no starvation). Operators occasionally flip
	// this on during incident response to drain `critical` faster.
	StrictPriority bool

	// Logger is required. Plumbed into asynq via an adapter so Asynq's
	// internal Info/Warn/Error lines land in the same slog stream as the
	// rest of the binary. Tests typically use a discard logger.
	Logger *slog.Logger

	// Metrics, when non-nil, receives gonext_jobs_* registrations. nil is
	// allowed so unit tests that don't care about Prometheus stay simple.
	// In production the worker binary always passes one.
	Metrics prometheus.Registerer
}

// validate normalizes a Config in place: applies defaults, checks
// invariants, returns the first violation. Called by New; callers
// constructing a Config for inspection can call it directly.
func (c *Config) validate() error {
	if c.Logger == nil {
		return errors.New("jobs/asynq.Config: Logger is required")
	}
	if len(c.Queues) == 0 {
		c.Queues = DefaultQueues()
	} else {
		for name, w := range c.Queues {
			if name == "" {
				return errors.New("jobs/asynq.Config: queue name must not be empty")
			}
			if w <= 0 {
				return errors.New("jobs/asynq.Config: queue " + name +
					" has non-positive weight; remove the entry or use a positive value")
			}
		}
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultConcurrency
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	if c.HealthCheckInterval <= 0 {
		c.HealthCheckInterval = defaultHealthCheckInterval
	}
	return nil
}
