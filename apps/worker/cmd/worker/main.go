// Command worker is the GoNext background-job runner (Asynq consumer).
//
// Status: skeleton — issue #1. Subsequent issues wire it to Redis,
// the task registry, the WASM plugin host, and the cron leader-election
// lease. See docs/12-jobs-cron.md and ADR 0010.
//
// This binary uses the shutdown orchestrator (packages/go/shutdown) to
// drain registered resources in LIFO order on SIGTERM. The worker's
// drain budget is intentionally larger than the API's because long-
// running jobs may need up to ~4 minutes to finish (see issue #112's
// AC). Today, with no real queue consumer yet, the orchestrator is
// configured but registers nothing; the wiring shape is final so when
// the real consumer + cron lease land they slot in directly.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
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
	// build identity to stdout) while we wire orchestration.
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

	orch, err := shutdown.New(shutdown.Options{
		Log:    logger,
		Budget: workerShutdownBudget,
	})
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	// Resource registration goes here as the worker grows. For #112
	// the ordering contract is locked in:
	//
	//   1. db.pool         (registered first → drains last)
	//   2. redis.client
	//   3. metrics.flusher
	//   4. audit.emitter
	//   5. cron.lease      (release lease key if held)
	//   6. queue.consumer  (stop accepting, drain in-flight) — LAST
	//
	// Each entry materializes in its own follow-up issue; the
	// orchestrator's API is stable so no churn here when they land.

	// For the skeleton, the worker exits immediately on ctx
	// cancellation — Wait returns nil because no closers are
	// registered. This keeps `kubectl rollout` clean even on the
	// pre-implementation binary.
	logger.Info("worker started (skeleton)", "drain_budget", workerShutdownBudget)
	if err := orch.Wait(ctx); err != nil {
		return fmt.Errorf("drain: %w", err)
	}
	logger.Info("worker stopped")
	return nil
}
