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

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	jobsasynq "github.com/Singleton-Solution/GoNext/packages/go/jobs/asynq"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
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

	srv, _, err := jobsasynq.New(redisOpt, jobsasynq.Config{
		Logger:  logger,
		Metrics: mreg.Prometheus(),
	})
	if err != nil {
		return fmt.Errorf("jobs/asynq: %w", err)
	}

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
