// Command worker is the GoNext background-job runner (Asynq consumer).
//
// Status: implements issue #256 — wires the Asynq server skeleton with
// the project's seven weighted queues (critical/webhook/email/media/
// migration/plugin/default) and registers it with the shutdown
// orchestrator. Subsequent issues add the task registry, the WASM
// plugin host, and the cron leader-election lease. See
// docs/12-jobs-cron.md and ADR 0010.
//
// This binary uses the shutdown orchestrator (packages/go/shutdown) to
// drain registered resources in LIFO order on SIGTERM. The worker's
// drain budget is intentionally larger than the API's because long-
// running jobs may need up to ~4 minutes to finish (see issue #112's
// AC).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/hibiken/asynq"

	workermedia "github.com/Singleton-Solution/GoNext/apps/worker/internal/media"
	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/cache/invalidator"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	jobsasynq "github.com/Singleton-Solution/GoNext/packages/go/jobs/asynq"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/scheduler"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
	"github.com/Singleton-Solution/GoNext/packages/go/media/storage"
	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
	"github.com/Singleton-Solution/GoNext/packages/go/observability/errortracker"
	gonextredis "github.com/Singleton-Solution/GoNext/packages/go/redis"
	"github.com/Singleton-Solution/GoNext/packages/go/shutdown"
)

const serviceName = "worker"

// workerShutdownBudget is the maximum time we wait for in-flight jobs
// to finish on SIGTERM. Worker drain is intentionally MUCH longer than
// the API's (30s) because background jobs may take minutes (image
// processing, report generation). K8s deployments must set a matching
// terminationGracePeriodSeconds to avoid SIGKILL mid-job.
//
// Issue #112 §AC names 240s for the worker; we use the same number.
const workerShutdownBudget = 240 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(2)
	}
}

func run(ctx context.Context) error {
	bi := buildinfo.Get(serviceName)

	// Skeleton emit — preserves the previous behavior (writing the
	// build identity to stdout) while we wire orchestration. Useful for
	// `docker run --rm gonext-worker` smoke tests.
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(bi); err != nil {
		return fmt.Errorf("encode buildinfo: %w", err)
	}

	logger, err := log.Setup(os.Stdout, log.Options{
		Service: serviceName,
		Version: bi.Version,
		Commit:  bi.Commit,
		Format:  log.FormatJSON,
		Redact:  true,
	})
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	orch, err := shutdown.New(shutdown.Options{
		Log:    logger,
		Budget: workerShutdownBudget,
	})
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	// Error tracking (#202). No-op when GONEXT_SENTRY_DSN is unset.
	// Registered before the queue consumer so the consumer's drain
	// can still report errors through a live transport. The cluster
	// env name is the same one Asynq's dashboards filter on.
	errTracker, errTrackerShutdown, errTrackerErr := errortracker.Init(errortracker.Options{
		Environment: string(cfg.Env),
		Release:     bi.Version,
		ServerName:  serviceName,
		Logger:      logger,
	})
	if errTrackerErr != nil {
		logger.Warn("errortracker: setup failed; continuing without error reporting",
			"err", errTrackerErr.Error())
	} else {
		orch.MustRegister(logger, "errortracker.client",
			func(stopCtx context.Context) error { return errTrackerShutdown(stopCtx) })
	}
	_ = errTracker // retained for task handlers that grow Capture sites in follow-ups

	// Metrics registry. The worker's /metrics listener lives in a
	// follow-up issue (the dedicated port wiring already exists in
	// packages/go/metrics); for now we just need the registerer so the
	// chassis can publish gonext_jobs_* even before the scrape endpoint
	// exists.
	mreg := metrics.NewRegistry()

	// Asynq Redis connection. ParseRedisURI accepts the same
	// redis://user:pass@host:port/db format as packages/go/redis. We
	// don't share a *redis.Client with anything else yet — the worker
	// is queue-only — so letting Asynq own its pool keeps connection
	// counts predictable (one pool, no surprises in `CLIENT LIST`).
	if cfg.Redis.URL == "" {
		return errors.New("REDIS_URL is required for the worker (asynq backend)")
	}
	redisOpt, err := asynq.ParseRedisURI(cfg.Redis.URL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}

	srv, mux, err := jobsasynq.New(redisOpt, jobsasynq.Config{
		Logger:  logger,
		Metrics: mreg.Prometheus(),
	})
	if err != nil {
		return fmt.Errorf("jobs/asynq: %w", err)
	}

	// Inspector-driven queue metrics (#172). Asynq's Inspector talks
	// to Redis and exposes the cluster-wide queue state — pending,
	// active, retry, archived, latency. Sampling once per /metrics
	// scrape keeps the wiring stateless; no background goroutine, no
	// shared mutable state, no shutdown ordering surprises beyond
	// closing the Inspector itself.
	//
	// The inspector owns its own Redis connection pool (separate from
	// the asynq.Server's), so we register it with the orchestrator
	// after the server's queue.consumer registration — LIFO drain
	// closes the inspector before the consumer, ensuring no in-flight
	// inspector call sees a half-closed Redis client.
	inspector := asynq.NewInspector(redisOpt)
	mreg.MustRegister(jobsasynq.NewInspectorCollector(inspector, jobsasynq.InspectorCollectorOptions{
		Queues: []string{
			jobsasynq.QueueCritical,
			jobsasynq.QueueWebhook,
			jobsasynq.QueueEmail,
			jobsasynq.QueueMedia,
			jobsasynq.QueueMigration,
			jobsasynq.QueuePlugin,
			jobsasynq.QueueDefault,
		},
		Logger: logger,
	}))
	orch.MustRegister(logger, "asynq.inspector", func(_ context.Context) error {
		return inspector.Close()
	})

	// Heavy-media tasks. Registered in stub mode for the boot-time
	// skeleton — the package consults the PATH and the wired storage
	// handles to decide between the real handler and the stub.
	// Production wiring (when the worker grows S3 access) replaces
	// the zero-value Deps with real Source/Sink implementations.
	//
	// See apps/worker/internal/media for the dispatch contract.
	mediaTaskRegistry := taskspec.NewRegistry()
	if _, err := workermedia.Register(mux, mediaTaskRegistry, workermedia.Deps{
		Logger: logger,
	}); err != nil {
		return fmt.Errorf("worker/media: register: %w", err)
	}
	// Postgres pool. Required for the cache-invalidation outbox
	// poller (#94), the content-scheduler/GC jobs (#143), and any
	// future task whose handler talks to the database. We let the
	// pool fail-fast at boot rather than waiting for the first job:
	// a worker that comes up green only to error every task is
	// indistinguishable from a healthy one until somebody looks.
	pool, err := db.New(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("db.New: %w", err)
	}
	orch.MustRegister(logger, "db.pool", func(context.Context) error {
		pool.Close()
		return nil
	})

	// Dedicated Redis client for the cache invalidator + content
	// scheduler. The asynq server has its own pool managed via
	// asynq.RedisClientOpt; we keep this one separate so the
	// invalidator's pub/sub lifetime is uncoupled from the queue
	// consumer's connection lifecycle.
	rdb, err := gonextredis.New(ctx, cfg.Redis, logger)
	if err != nil {
		return fmt.Errorf("redis.New: %w", err)
	}
	orch.MustRegister(logger, "redis.client", func(context.Context) error {
		return rdb.Close()
	})

	// Cache invalidator: drains the cache_invalidations outbox
	// shipped by 000030 and republishes each row on the
	// gonext:cache:invalidate pub/sub channel. We start the worker
	// in a goroutine and shut it down via the orchestrator so the
	// drain budget covers an in-flight poll cycle.
	invWorker := invalidator.New(pool, rdb, invalidator.WithLogger(logger))
	invCtx, invCancel := context.WithCancel(ctx)
	invDone := make(chan struct{})
	go func() {
		defer close(invDone)
		if err := invWorker.Run(invCtx); err != nil {
			logger.Warn("cache invalidator exited with error", "err", err.Error())
		}
	}()
	orch.MustRegister(logger, "cache.invalidator", func(context.Context) error {
		invCancel()
		<-invDone
		return nil
	})

	// Registration order (locked in by issue #112):
	//
	//   1. db.pool         (registered first → drains last)        — future
	//   2. redis.client                                            — future
	//   3. metrics.flusher                                         — future
	//   4. audit.emitter                                           — future
	//   5. cron.lease      (release lease key if held)             — future
	//   6. queue.consumer  (stop accepting, drain in-flight) — LAST
	//
	// queue.consumer is registered last so it drains FIRST. That stops
	// new task pickup before the Redis client (which it depends on)
	// closes underneath it.
	orch.MustRegister(logger, "queue.consumer", srv.Close)

	// Task handlers register on srv.Mux() as follow-up issues land:
	//   srv.Mux().HandleFunc(email.TaskTypeSend, email.HandleSend)
	// For #256 we ship the skeleton with the NotFound default so
	// unknown tasks NACK cleanly instead of panic'ing the worker pool.
	//
	// The storage abort-orphans sweep is the first task that lands
	// here (issue #23). We build the storage driver from the same
	// config the API uses, wire the handler onto the asynq mux via
	// taskspec.Dispatch, and register the cron schedule on the
	// process-wide cron registry. The scheduler itself runs in a
	// separate cron-leader goroutine (issue #258); this main wiring
	// only declares the spec + schedule so the leader has something
	// to fire.
	mediaDriver, err := storage.New(ctx, storage.Options{
		S3: storage.S3Config{
			Endpoint:  cfg.Storage.Endpoint,
			Region:    cfg.Storage.Region,
			Bucket:    cfg.Storage.Bucket,
			AccessKey: cfg.Storage.AccessKey,
			SecretKey: cfg.Storage.SecretKey,
			UseSSL:    cfg.Storage.UseSSL,
			PathStyle: cfg.Storage.PathStyle,
		},
	})
	if err != nil {
		// Non-fatal: the worker can still drain non-media tasks. We
		// log loudly so an operator can see the misconfiguration and
		// fix the env vars rather than wondering why orphans pile up.
		logger.Warn("media storage driver init failed; abort-orphans task will be skipped",
			"err", err.Error(),
		)
	} else {
		spec, err := storage.NewAbortOrphansSpec(storage.AbortOrphansSpecOptions{
			Driver: mediaDriver,
			Logger: logger,
		})
		if err != nil {
			logger.Warn("storage abort-orphans spec build failed",
				"err", err.Error(),
			)
		} else {
			reg := taskspec.Default()
			if err := reg.Register(spec); err != nil &&
				!errors.Is(err, taskspec.ErrAlreadyRegistered) {
				logger.Warn("register abort-orphans task failed",
					"err", err.Error(),
				)
			}
			// Dispatch onto the asynq mux so the consumer side runs
			// the handler when the leader enqueues it.
			taskspec.Dispatch(srv.Mux(), reg)
			// Cron-side registration: declare WHEN the task fires.
			// We build a fresh registry per process — the cron
			// package deliberately does not ship a Default()
			// singleton; ownership is the binary's. A follow-up
			// issue mounts the scheduler against this registry once
			// the cron-leader lease (#258) lands.
			cronReg := cron.NewRegistry()
			cronSpec := storage.NewAbortOrphansCron()
			if err := cronReg.Register(cronSpec); err != nil &&
				!errors.Is(err, cron.ErrAlreadyRegistered) {
				logger.Warn("register abort-orphans cron failed",
					"err", err.Error(),
				)
			}
			_ = cronReg // pinned for the scheduler hookup below
			logger.Info("storage abort-orphans cron registered",
				"task", storage.AbortOrphansTaskName,
				"cron", storage.AbortOrphansCronName,
				"schedule", storage.AbortOrphansSchedule,
			)

			// Content scheduler + GC (#143). Both tasks share the
			// worker's pgx pool. The publisher fires every minute
			// and flips status=scheduled rows whose scheduled_for
			// has elapsed; the GC fires daily at 03:30 UTC and
			// hard-deletes trash older than 30 days.
			//
			// We register against the same cronReg as the storage
			// sweep so the eventual cron-leader (#258) sees one
			// merged schedule, and against taskspec.Default() so
			// the asynq mux dispatch already in place handles the
			// task type.
			if err := scheduler.SeedDefaults(taskspec.Default(), cronReg, scheduler.SeedOptions{
				Pool:   pool,
				Logger: logger,
			}); err != nil {
				logger.Warn("scheduler seed failed", "err", err.Error())
			} else {
				taskspec.Dispatch(srv.Mux(), taskspec.Default())
				logger.Info("content scheduler + gc registered",
					"publisher_task", scheduler.PublisherTaskName,
					"publisher_schedule", scheduler.PublisherSchedule,
					"gc_task", scheduler.GCTaskName,
					"gc_schedule", scheduler.GCSchedule,
				)
			}
		}
	}

	logger.Info("worker started",
		"drain_budget", workerShutdownBudget,
		"redis_url_present", cfg.Redis.URL != "",
	)

	// Asynq's Run blocks until the server's own Shutdown is called.
	// Our shutdown.Orchestrator triggers that via srv.Close, so the
	// expected lifecycle is:
	//
	//   1. SIGTERM arrives
	//   2. orch.Wait → Drain → srv.Close → asynq Stop+Shutdown
	//   3. Run returns nil here, and the goroutine exits
	//   4. orch.Wait returns nil
	//
	// runErrCh surfaces failures from Run that aren't triggered by the
	// orchestrator (e.g. Redis lost mid-flight and Asynq self-aborts).
	// We don't block on it directly because the orchestrator owns the
	// shutdown story; we surface the error after Wait returns.
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- srv.Run() }()

	waitErr := orch.Wait(ctx)
	// Run has either already returned (because Close triggered
	// Shutdown) or will return imminently — give it a short grace
	// window. We don't want to leak the goroutine on the unlikely path
	// where Asynq held a goroutine past its own ShutdownTimeout.
	var runErr error
	select {
	case runErr = <-runErrCh:
	case <-time.After(5 * time.Second):
		logger.Warn("asynq Run did not return within grace window after drain")
	}

	if waitErr != nil {
		return fmt.Errorf("drain: %w", waitErr)
	}
	if runErr != nil {
		return fmt.Errorf("asynq run: %w", runErr)
	}
	logger.Info("worker stopped")
	return nil
}
