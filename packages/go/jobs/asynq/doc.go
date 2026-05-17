// Package asynq is GoNext's thin chassis around hibiken/asynq.
//
// It owns the boring, repetitive parts of standing up an Asynq server so
// every binary that consumes the job system (today: apps/worker; in the
// near future: apps/api running embedded for single-binary deploys) wires
// up the same way: same queue topology, same shutdown contract, same
// logger plumbing, same Prometheus surface.
//
// The package is deliberately small. It does NOT register task handlers,
// it does NOT define task payloads, and it does NOT know about the worker
// binary. That separation lets follow-up issues bolt task registries
// (#257), the cron lease (#258), the WASM plugin host (#259) and the DLQ
// scanner (#260) on without touching the chassis.
//
// # Queue topology
//
// Seven weighted queues, per the project's queue policy in
// docs/12-jobs-cron.md (issue #256 reconciles the doc's earlier names to
// the final seven):
//
//	critical   10   password reset, 2FA, signup verification
//	webhook     5   outbound webhook delivery
//	email       5   transactional email
//	media       3   image variants, video transcode
//	migration   2   WP importer batches
//	plugin      2   plugin-enqueued jobs (sandboxed)
//	default     1   everything else / fallback
//
// Asynq schedules tasks with probability proportional to the queue's
// weight, so `critical` (10) is picked roughly 36% of the time when every
// queue has work, but `default` (1) still gets ~3.6% — no starvation.
//
// # Wiring
//
// Typical worker boot:
//
//	rdb, err := redisp.New(ctx, cfg.Redis, logger)        // pool we own
//	if err != nil { return err }
//	orch.Register("redis.client", shutdown.CloserFromIO(rdb))
//
//	srv, mux, err := jobsasynq.New(asynq.RedisClientOpt{...}, jobsasynq.Config{
//	    Logger: logger,
//	    Metrics: reg,                                      // optional
//	})
//	if err != nil { return err }
//	orch.Register("queue.consumer", jobsasynq.Closer(srv)) // Stop+Shutdown
//
//	// Task handlers register against mux as separate issues land:
//	// mux.HandleFunc(email.TaskTypeSend, email.HandleSend)
//
//	go func() {
//	    if err := srv.Run(mux); err != nil { ... }
//	}()
//	return orch.Wait(ctx)
//
// # Shutdown contract
//
// The Asynq server runs in its own goroutine via Run. On SIGTERM the
// shutdown orchestrator (packages/go/shutdown) invokes the registered
// Closer in LIFO order, AFTER everything that produces work (the HTTP
// listener for in-process clients) has stopped. The Closer does two
// things, in this order:
//
//  1. srv.Stop()      — stop pulling new tasks off Redis.
//  2. srv.Shutdown()  — wait for in-flight handlers up to
//     Config.ShutdownTimeout, then NACK the rest back to Redis so a
//     surviving replica (or the same replica on restart) picks them up.
//
// The default ShutdownTimeout (3m) is intentionally well under the
// worker's overall drain budget (workerShutdownBudget = 240s in main),
// leaving headroom for the surrounding closers (Redis client, metrics
// flusher, audit emitter) and a safety margin before K8s SIGKILLs the pod.
//
// # Default handler
//
// New attaches asynq.NotFoundHandler as the mux's fallback. The standard
// library `http.ServeMux` returns 404 for unmatched routes; the Asynq
// equivalent NACKs the task with ErrHandlerNotFound, which Asynq counts as
// a failure and (per the task's MaxRetry) eventually moves to the archive
// queue. This is the right default: an unknown task type is a deploy/code
// skew bug, not a silent drop. The metrics surface counts these as
// failures so operators see the spike.
//
// # Observability
//
// metrics.go registers four Prometheus metrics under the gonext_jobs_
// namespace:
//
//	gonext_jobs_processed_total{queue}  — handler returned nil
//	gonext_jobs_failed_total{queue}     — handler returned non-nil
//	gonext_jobs_inflight{queue}         — tasks currently executing
//	gonext_jobs_unknown_total{queue}    — handler-not-found dispatches
//
// Each is keyed by queue (one of the seven names) so dashboards filter
// per-queue without exploding cardinality.
//
// # Health
//
// health.go exposes Healthy() reporting the latest Redis ping result, so
// /readyz integrations (packages/go/httpx) can fail readiness when the
// queue backend is unreachable. The freshness window is one Asynq
// HealthCheckInterval (15s default).
package asynq
