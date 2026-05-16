# ADR 0010: Asynq (Redis-backed) is the background-job queue

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 12 (jobs and cron), doc 00 §2 (stack table)
- **Informed**: worker authors, plugin authors who enqueue jobs

## Context

GoNext runs many things in the background: outbound email (welcome, password reset, comment notifications), webhook delivery, cache invalidation fan-out, ISR revalidation, image variant generation, video transcoding, full-text-search reindexing, audit log rollup, WordPress migration batches, and plugin-enqueued jobs. Doc 12 catalogs the full set; the inventory has ~30 task types across 7 logical queues.

The shape of the workload:

- **Burst-heavy.** A single "publish" can fan out to hundreds of cache invalidations, dozens of ISR revalidations, and a handful of webhook deliveries. Capacity has to absorb bursts without dropping critical-path work.
- **Mixed priority.** Password-reset email is a "block the user's flow" task. Audit-log rollup is "do whenever." Both need to coexist without the loud one starving the quiet one or vice versa.
- **Mixed durability needs.** Webhook delivery and email need durable retries with exponential backoff. Cache invalidation needs unique-key dedup (a publish that fires three identical invalidations should run one). Cron jobs need leader-election so only one replica fires the schedule.
- **Plugin-submitted jobs.** Plugins (ADR 0005) enqueue jobs via a host ABI call. Their jobs land in a sandboxed queue with per-plugin rate limits (doc 12 §4).
- **Single-binary deploy must work.** Self-host-first (proposal S5) means the smallest deploy shape is one Go binary plus Postgres plus Redis. The job system has to be embeddable in the same process for that shape, and scalable out to dedicated worker replicas for larger deploys.

The candidates:

- **Asynq.** Go-native, Redis-backed. Multiple weighted queues with strict or weighted priority. Per-task UniqueKey (TTL-scoped) for in-flight dedup. Retries with jittered exponential backoff, configurable per enqueue and per task type. Archive on terminal failure (our DLQ). Built-in scheduler with Redis-leased leader election. Server affinity via queue assignment per worker pool. Prometheus-friendly metrics. Single Redis backend, which we already operate for cache and sessions.
- **Temporal.** Best-in-class for durable, long-running stateful workflows. Requires its own cluster: Cassandra or MySQL for history, separate history service, separate workers, separate UI. For a self-host-first CMS the operational surface area triples. We do not need long-running stateful workflows; we need a job queue.
- **River.** Postgres-backed Go job queue. Newer (2023+). Compelling because one fewer external dependency. Less mature unique-task and leader-election stories. Worth re-evaluating in v2 if the Redis dependency becomes a liability.
- **Faktory.** Polyglot job server, Go client is community-maintained, separate daemon to operate. Useful in polyglot shops; we are mostly Go.
- **RabbitMQ / NATS JetStream.** Message brokers, not job queues. We would have to build retries, scheduled tasks, cron, DLQ, and unique-key dedup on top. Asynq has all of that.
- **In-process goroutine workers.** A non-starter past v0: crash on enqueue loses every queued job, no cron leader story across replicas, no observability, no DLQ.
- **Homegrown on Redis (BRPOPLPUSH + Lua scripts).** Reinvents Asynq, badly.

Doc 12 §1.1 walks through these rejections; doc 12 §19 revisits them.

## Decision

Asynq (Redis-backed, Go-native) is the background-job queue for all background work in GoNext: outbound email, webhook delivery, cache invalidation fan-out, ISR revalidation, image variant generation, video transcoding, search reindexing, audit rollup, WP migration batches, and plugin-enqueued jobs. We use Asynq's multiple weighted queues, UniqueKey deduplication, exponential backoff with jitter, Archive-as-DLQ semantics, and Redis-leased scheduler leader election. The same code path runs embedded in the API binary for single-binary deploys and as a standalone `gonext worker` binary for split deploys.

## Consequences

### Positive

- **Single Redis dependency.** We already operate Redis for cache, sessions, and rate limiting (doc 00 §2). No new infrastructure.
- **Weighted queues prevent starvation.** Doc 12 §2 defines seven queues with explicit weights: `critical=6, default=4, media=3, cache=3, migrate=2, plugins=2, low=1`. High-priority work outruns low-priority work without ever fully starving the low queue.
- **UniqueKey deduplication.** Burst-heavy fan-outs (cache invalidation, ISR revalidate) use `cache.invalidate.tag_fanout:{tag}` keys with short TTL so duplicate fires from concurrent mutations collapse to one job.
- **Cron with leader election out of the box.** Asynq's scheduler uses a Redis-leased lock so exactly one replica fires the schedule across N replicas. Doc 12 §8.1.
- **Per-queue worker binding.** Production deploys can split workers by queue set (`worker-media` binds `media,default`; `worker-migrate` binds `migrate`). One topology supports single-binary, split-binary, and HA shapes.
- **Plugin job isolation.** Plugin jobs land on the `plugins` queue with per-plugin partitioning, concurrency caps, and rate limits (doc 12 §4). One bad plugin cannot drown user-facing work.

### Negative

- **Redis is now load-bearing for durability.** Lose Redis, lose queued jobs (subject to Redis's own persistence — AOF/RDB). Mitigation: run Redis with AOF, monitor `last_save`, document the operational expectation. We do not run Redis as ephemeral.
- **Redis dataset grows with archive retention.** Failed jobs in the archive (DLQ) accumulate. Doc 12 §6.2 defines retention; the operator has to size Redis accordingly.
- **No durable saga / long-running workflow primitive.** A multi-step pipeline that has to survive process restarts mid-step (think a video transcode with checkpoints) builds checkpointing into the task handler itself. Doc 12 §9.3 documents the chunking pattern.
- **Asynq is a moderately small project.** Active, well-maintained, but not the scale of Sidekiq or Celery. Risk is real but bounded by Go's `replace` directive escape hatch.

### Neutral / accepted tradeoffs

- Plugins never enqueue directly onto `critical` or `default`. Every plugin job lands on `plugins`. This is part of the plugin trust model (ADR 0005, ADR 0012).
- Idempotency is the task author's responsibility. Doc 12 §7 gives a recommended pattern (per-task idem keys with TTL). The queue does not guarantee exactly-once; we guarantee at-least-once and require handlers to be idempotent.
- The same Go binary contains both the HTTP server and the worker code. Single-binary deploys run both; split deploys disable the worker server on `api` replicas via config.

## Alternatives considered

### Option A: Temporal
- Rejected. Requires its own cluster (history service + persistence + workers + UI) — triples the ops surface area for a self-host-first project. Built for durable long-running workflows, which we do not have. Doc 12 §19.2.

### Option B: River (Postgres-backed)
- Rejected for v1. Compelling "one fewer dependency" story, but younger ecosystem and a less mature unique-task / leader-election story. We already operate Redis. Worth re-evaluating in v2 if the Redis dependency becomes a liability. Doc 12 §19.3.

### Option C: Homegrown queue
- Rejected. Reinvents Asynq, badly. The list of features we would have to build (retries with backoff, unique keys, cron leader election, DLQ, observability) is exactly Asynq's surface. Doc 12 §1.1.

### Option D: RabbitMQ / NATS JetStream
- Rejected. A message broker is not a job queue. We would build retries, scheduled jobs, cron, DLQ, and unique-key dedup on top of it. Asynq already has all of that. Doc 12 §19.5.

### Option E: Faktory
- Rejected. Polyglot positioning is wasted on a mostly-Go project. Separate daemon to operate; Go client is community-maintained. Doc 12 §1.1.

### Option F: In-process goroutine workers
- Rejected. Crash loses queued jobs; no cross-replica cron; no DLQ; no observability. Acceptable for `go test` and dev only. Doc 12 §19.4.

## References

- Design doc: `docs/12-jobs-cron.md` §1 (why Asynq), §1.1 (rejected alternatives), §2 (queue topology), §19 (revisited rejections)
- Design doc: `docs/00-architecture-overview.md` §2 (stack table)
- Asynq: https://github.com/hibiken/asynq
- Related ADRs: ADR 0005 (plugin runtime that enqueues plugin jobs), ADR 0011 (cache invalidation worker drains a Postgres outbox, separate from Asynq), ADR 0012 (capability gates plugin enqueue)
