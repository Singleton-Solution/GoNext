package asynq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/hibiken/asynq"
)

// Server is the chassis's wrapper around *asynq.Server. We keep the
// shape minimal — callers use the bare *asynq.Server returned by New for
// everything that isn't health or shutdown, and reach for these methods
// only when integrating with the orchestrator.
type Server struct {
	srv     *asynq.Server
	mux     *asynq.ServeMux
	health  *healthState
	metrics *metrics
	logger  *slog.Logger

	// done is closed exactly once by Close. Run blocks on it; closing
	// it from a goroutine that is not the one running Run is what
	// makes Run a goroutine-cancellable blocking call (the upstream
	// asynq.Run only unblocks on signals).
	done     chan struct{}
	closeOne sync.Once
}

// New constructs an Asynq server + ServeMux pair wired with:
//
//   - the seven weighted queues from Config (or the canonical defaults),
//   - a NotFoundHandler fallback so unknown task types NACK instead of
//     panic'ing the worker pool,
//   - slog plumbing so Asynq's internal Info/Warn/Error lines join the
//     binary's structured stream,
//   - Prometheus middleware that observes inflight/processed/failed
//     per queue (skipped if Config.Metrics is nil),
//   - a HealthCheckFunc that drives Healthy() for /readyz integration.
//
// The Server is returned in "ready but not started" state. Callers must
// call Run (blocking) or Start (background) to begin pulling tasks. The
// returned ServeMux is the same instance Asynq will dispatch through;
// task handlers register on it before Run.
//
// connOpt is the Asynq Redis connection option. We accept this rather
// than building it from packages/go/config so callers retain control of
// the Redis client lifecycle. The shutdown ordering contract in
// apps/worker registers the Redis client AFTER the queue consumer (LIFO
// drain order is consumer-first), so Asynq can keep using the pool
// during its own shutdown without surprises.
func New(connOpt asynq.RedisConnOpt, cfg Config) (*Server, *asynq.ServeMux, error) {
	if connOpt == nil {
		return nil, nil, errors.New("jobs/asynq.New: redis connection option is required")
	}
	if err := cfg.validate(); err != nil {
		return nil, nil, fmt.Errorf("jobs/asynq.New: %w", err)
	}

	logger := cfg.Logger.With(slog.String("component", "asynq"))
	m := newMetrics(cfg.Metrics)
	h := newHealthState(cfg.HealthCheckInterval)

	mux := asynq.NewServeMux()
	// Asynq's ServeMux already returns a NotFoundHandler stub for
	// unmatched task types (servemux.go:65) that errors with
	// ErrHandlerNotFound. That's the right default behavior — Asynq
	// counts the error against the task's MaxRetry budget and
	// eventually archives it, surfacing deploy/code skew rather than
	// silently dropping the task. We don't need to override it.
	//
	// The middleware below special-cases ErrHandlerNotFound so we get
	// a dedicated gonext_jobs_unknown_total counter, distinct from
	// "a real handler returned an error".
	mux.Use(metricsMiddleware(m))

	srv := asynq.NewServer(connOpt, asynq.Config{
		Concurrency:         cfg.Concurrency,
		Queues:              cfg.Queues,
		StrictPriority:      cfg.StrictPriority,
		ShutdownTimeout:     cfg.ShutdownTimeout,
		HealthCheckInterval: cfg.HealthCheckInterval,
		HealthCheckFunc:     h.record,
		Logger:              slogAdapter{logger: logger},
		// LogLevel is left at Asynq's default (InfoLevel). We rely on
		// our slog handler's own level filter — having Asynq filter
		// again would mean two places to tune verbosity.
	})

	return &Server{
		srv:     srv,
		mux:     mux,
		health:  h,
		metrics: m,
		logger:  logger,
		done:    make(chan struct{}),
	}, mux, nil
}

// Asynq returns the underlying *asynq.Server. Use this only for things
// the chassis doesn't surface directly (Asynq's Inspector API, Ping for
// callers that want a synchronous ping outside the health goroutine).
// Day-to-day code should not need this.
func (s *Server) Asynq() *asynq.Server { return s.srv }

// Mux returns the ServeMux for handler registration. Identical to the
// second return value of New; surfaced as a method too so callers that
// pass Server around can register without holding a separate reference.
func (s *Server) Mux() *asynq.ServeMux { return s.mux }

// Healthy returns true when the most recent Asynq→Redis ping succeeded
// within the staleness window. Safe for high-frequency calls (atomic
// load, no allocations). Intended target: /readyz handlers.
func (s *Server) Healthy() bool { return s.health.Healthy() }

// Run starts the server and blocks until Close is invoked from another
// goroutine. Unlike asynq.Server.Run (which installs its OWN signal
// handler for SIGTERM/SIGINT/SIGTSTP and then calls Shutdown), the
// chassis defers signal handling to packages/go/shutdown — having two
// independent signal handlers in the same process is exactly the
// pathology shutdown.Orchestrator was built to avoid.
//
// The implementation is asynq.Server.Start (non-blocking) plus a
// channel wait that the chassis's own Close closes. The blocking
// semantics keep main()'s wiring symmetric with the API binary's
// http.Server.ListenAndServe model: one goroutine per long-lived
// component, all surfaced through the shutdown orchestrator.
func (s *Server) Run() error {
	if err := s.srv.Start(s.mux); err != nil {
		return err
	}
	<-s.done
	return nil
}

// Close drains the server gracefully and returns nil. Wraps the
// two-phase Asynq shutdown:
//
//  1. Stop()     — stop pulling new tasks off Redis.
//  2. Shutdown() — wait for in-flight handlers up to ShutdownTimeout,
//     then NACK the rest back to Redis.
//
// Designed to be passed to shutdown.Orchestrator.Register as a Closer.
// The ctx is accepted to match the Closer signature; we don't propagate
// it to asynq.Shutdown because Asynq's shutdown is already bounded by
// Config.ShutdownTimeout (set at construction). Logging the ctx state on
// entry helps operators correlate slow drains with cluster-wide
// cancellations.
func (s *Server) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		s.logger.Warn("asynq shutdown invoked with canceled context; running detached",
			slog.String("err", err.Error()),
		)
	}
	s.closeOne.Do(func() {
		s.srv.Stop()
		s.srv.Shutdown()
		close(s.done)
	})
	return nil
}

// metricsMiddleware wraps every dispatched task with start/finish
// observations. Asynq middleware runs around the handler (including the
// built-in NotFound fallback for unknown task types), so this is the
// right hook to keep inflight accurate even if a handler panics —
// Asynq recovers panics and reports them as handler errors, which is
// what observeFinish records as a failure (by design).
//
// We inspect the returned error: errors.Is(err, asynq.ErrHandlerNotFound)
// means a task arrived with no registered handler. That gets its own
// counter so operators can distinguish deploy skew (we shipped a
// publisher before its consumer) from real handler errors.
func metricsMiddleware(m *metrics) asynq.MiddlewareFunc {
	return func(h asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			q, _ := asynq.GetQueueName(ctx)
			if q == "" {
				q = QueueDefault
			}
			m.observeStart(q)
			err := h.ProcessTask(ctx, t)
			if err != nil && errors.Is(err, asynq.ErrHandlerNotFound) {
				m.observeUnknown(q)
			}
			m.observeFinish(q, err)
			return err
		})
	}
}

// slogAdapter bridges asynq.Logger (5 methods, all variadic ...interface{})
// to *slog.Logger. Asynq emits a handful of lifecycle lines per server
// ("starting", "stopping", "ping failed"); routing them through slog keeps
// the binary's log stream homogeneous (json everywhere in production).
//
// We use slog's Log method with an explicit level rather than calling
// Info/Warn/Error directly because asynq.Fatal must NOT call os.Exit —
// the chassis's drain orchestrator is the authority on process exit, and
// a stray os.Exit from a library would skip every registered closer.
// Mapping Fatal to slog.LevelError preserves the severity in logs
// without surrendering process control.
type slogAdapter struct {
	logger *slog.Logger
}

func (a slogAdapter) log(level slog.Level, args ...interface{}) {
	a.logger.Log(context.Background(), level, fmt.Sprint(args...))
}

func (a slogAdapter) Debug(args ...interface{}) { a.log(slog.LevelDebug, args...) }
func (a slogAdapter) Info(args ...interface{})  { a.log(slog.LevelInfo, args...) }
func (a slogAdapter) Warn(args ...interface{})  { a.log(slog.LevelWarn, args...) }
func (a slogAdapter) Error(args ...interface{}) { a.log(slog.LevelError, args...) }
func (a slogAdapter) Fatal(args ...interface{}) { a.log(slog.LevelError, args...) }
