# Background Jobs & Cron

> Doc #12 in the WordPress-clone design series. **Read [`00-architecture-overview.md`](00-architecture-overview.md) first.**
>
> This document owns the **Asynq-based background job system**: queue topology, task catalog, retry/idempotency contracts, cron leader election, plugin job scoping, DLQ, backpressure, and operational tooling.
>
> Gap review §A4: prior to this doc, seven other docs (02 plugins, 04 editor, 05 admin/webhooks, 06 auth, 07 media, 08 migration, plus core cron) invoked "Asynq" without anyone defining what we run, how, or with what guarantees. Doc 12 closes that gap. All other docs link **into** this one for the job-related contracts they depend on.
>
> **Reader**: senior backend engineer who has shipped and operated a background-job system in production. We skip the "what is a queue" preamble.

---

## 1. Why Asynq

Asynq is a Go-native, Redis-backed task queue with first-class support for the features a CMS-grade workload needs: priorities, weighted queues, retries with backoff + jitter, scheduled tasks, unique-key tasks, periodic (cron) schedules, dead-lettering (archive), inspection/CLI, and a Prometheus-friendly metrics surface.

Concretely we lean on:

- **Multiple weighted queues** with strict or weighted scheduling (we use weighted; see §2). This lets `critical` always outrun `low` without starving it.
- **Per-task UniqueKey** (TTL-scoped) to dedupe in-flight tasks — essential for fanouts (cache invalidation, ISR revalidate, search reindex).
- **Retry with jittered exponential backoff**, configurable per enqueue and per task type via `MaxRetry` + custom `RetryDelayFunc`.
- **Archive on failure** (Asynq's terminal state for exhausted retries) — we surface this as our DLQ.
- **Built-in scheduler** with Redis-leased leader election — exactly one cron dispatcher across N replicas (see §8).
- **Server affinity** via queue assignment per worker pool. A migration replica binds only `migrate`; a media worker binds `media` + `default`. This lets us shape capacity per workload.
- **Single Redis backend** — we already operate Redis for sessions and cache (doc 00 §2). One less moving part.

### 1.1 Alternatives rejected

| Option | Why rejected |
|---|---|
| **Temporal** | Best-in-class durable workflows, but requires its own cluster (Cassandra/MySQL + history service + workers + UI). For a self-host-first CMS that ships as a single Go binary + Postgres + Redis, dragging in Temporal triples our ops surface. We do not need long-running stateful workflows; we need a job queue. |
| **River** | Postgres-backed (compelling — one fewer store), but young and lacks mature unique-task / leader-election story we get for free in Asynq. We already have Redis. Worth re-evaluating in v2 if the Redis dependency becomes a liability. |
| **Faktory** | Polyglot, but a separate Faktory server is one more daemon to operate, and the Go client is community-maintained. |
| **RabbitMQ / NATS JetStream** | A message broker is not a job queue. We would need to build retries/scheduled/cron/DLQ/unique on top. Not justified when Asynq already has all of it. |
| **In-process goroutine workers** | Tempting for v1 but a non-starter: crash on enqueue loses every queued job, no cron leader story across replicas, no observability, no DLQ. Strictly worse than running Asynq workers in the same binary (which is what we do for single-node deploys). |
| **Homegrown over Redis (BRPOPLPUSH + Lua)** | Possible but reinvents Asynq badly. |

### 1.2 Deployment shapes

- **Single binary (small site).** The API binary runs an embedded Asynq server with all queues enabled. Cron scheduler also in-process. Redis required; no separate worker fleet.
- **Split (production).** API binary serves HTTP only. A separate `gonext worker` binary runs Asynq servers. Worker replicas can be specialized by queue set (`--queues=media,default` vs `--queues=migrate`).
- **High-availability.** N worker replicas + 1 elected scheduler. Scheduler leadership is Redis-leased; any replica can win.

The library is the same in all three shapes; the difference is what we start at boot.

---

## 2. Queue topology

We run **seven logical queues** at known weighted priorities. Weights are summed and Asynq picks each next task with probability proportional to the queue's weight: higher = served more often, but no queue ever fully starves.

| Queue | Weight (priority) | Worker concurrency (per replica) | Used for |
|---|---|---|---|
| `critical` | 6 | 4 | password reset, 2FA delivery, email verification |
| `default` | 4 | 16 | webhooks, transactional email, ISR fanout |
| `media` | 3 | 8 (CPU-bound, see below) | image variants, video transcode |
| `cache` | 3 | 8 | cache invalidation fan-out, warmup |
| `migrate` | 2 | 4 (I/O-bound) | importer batches |
| `plugins` | 2 | dynamic per-plugin (see §4) | plugin-enqueued jobs |
| `low` | 1 | 4 | cleanup, retention, RUM aggregation |

Total concurrency per replica is the **sum of the queue concurrencies**, which Asynq treats as one shared worker pool size: roughly 44 by default, tunable via `GONEXT_WORKER_CONCURRENCY`.

### 2.1 Per-queue rationale

**`critical` (weight 6).** Reserved for tasks that block a real human's flow: password reset emails, 2FA codes, email verification on signup. Latency target: enqueue-to-delivered p95 < 5s. Concurrency is small (4) because the volume is tiny; the priority is what matters. Never sheds under backpressure (see §12). Never auto-retried with long backoffs; failed delivery surfaces immediately to the user with "we couldn't send — try again."

**`default` (weight 4).** The general transactional queue: webhook deliveries, comment notifications, "post published" emails, Next.js ISR revalidate fanout. High concurrency (16) because the unit of work is small and mostly I/O (HTTP out). This is where the bulk of normal traffic lives.

**`media` (weight 3).** CPU-heavy image variant generation, video transcoding (handing off to ffmpeg subprocess), thumbnail extraction. Concurrency tuned to CPU: default 8 per replica but operators should set `media` concurrency near vCPU count. Media worker replicas can be scaled horizontally and bound to only the `media` queue to isolate CPU contention from the latency-sensitive `default` queue. Video transcode tasks **chunk** themselves (see §9) so a single 30-minute file never holds one worker for 30 minutes.

**`cache` (weight 3).** Fan-out of cache invalidations and tag purges (doc 07). These are bursty (one publish → hundreds of tag invalidations → ISR revalidate on every page that references the post). Separated from `default` so a publish storm does not starve user-facing webhook deliveries. Use UniqueKey extensively (§10) — duplicate invalidations are pure waste.

**`migrate` (weight 2).** Importer batches from doc 08. Migrations are slow, I/O-heavy, and the user kicked them off knowing it would take time. Lower priority means a migration cannot starve a user clicking "publish." Small concurrency (4) because each batch is itself parallelizing reads from the WP XML source. A dedicated migration worker replica can boost this.

**`plugins` (weight 2).** Plugin-enqueued jobs from doc 02. Plugins are untrusted; we **do not** let them push directly onto `critical` or `default`. Instead, every plugin job lands here. Per-plugin partitioning, per-plugin concurrency caps, and per-plugin rate limits prevent one bad plugin from drowning the queue. See §4.

**`low` (weight 1).** Janitorial: revisions purge, autosave purge, audit archive, RUM aggregation, DLQ scans, orphan media GC. Not user-visible. Tiny concurrency. First to shed under backpressure.

### 2.2 Worker→queue binding

```
critical=4 default=16 media=8 cache=8 migrate=4 plugins=8 low=4
```

is the default for the all-in-one worker binary. Production runs typically split:

- **api** replicas: enqueue-only, no workers
- **worker-general** replicas: `critical, default, cache, plugins, low`
- **worker-media** replicas: `media, default` (so a media worker can chip in on default when idle)
- **worker-migrate** replicas: `migrate` only, scaled to 1–2 during imports, scaled to 0 at rest

The binding is `GONEXT_WORKER_QUEUES=critical:4,default:16,...` — a CSV of `queue:concurrency`. Asynq's queue weights are global config in `config.yaml`.

---

## 3. Task type catalog

Every task in the system. Each row is a contract: the task type name (used by the registry), the queue it lands on, payload shape, owner doc, retry policy, idempotency strategy, and target p50/p95 wall-clock.

> Conventions: payload is a JSON object. `idem_key` is always optional in the type but the row says when it is **required**. p50/p95 are **target** durations measured per task handler invocation, not end-to-end.

| Task type | Queue | Payload (essentials) | Owner doc | Retry | Idempotency | p50 / p95 |
|---|---|---|---|---|---|---|
| `email.send` | `default` | `{template, to, locale, vars, idem_key}` | 12 | exp backoff base 5s, max 1h, 10 attempts | `idem_key=(template, to, content_hash)` required, TTL 24h | 200ms / 2s |
| `email.password_reset` | `critical` | `{user_id, token_ref, ip}` | 06 | base 2s, max 5m, 5 attempts | `(user_id, token_ref)` TTL 1h | 200ms / 2s |
| `email.verify` | `critical` | `{user_id, token_ref}` | 06 | base 2s, max 5m, 5 attempts | `(user_id, token_ref)` TTL 24h | 200ms / 2s |
| `email.comment_notify` | `default` | `{comment_id, recipient_user_id}` | 01 | base 30s, max 6h, 8 attempts | `(comment_id, recipient_user_id)` | 200ms / 1s |
| `webhook.deliver` | `default` | `{subscription_id, event_id, url, body_ref, hmac_secret_ref}` | 12 (mechanics) / 05 (surface) | custom schedule §14, 8 attempts | `(subscription_id, event_id)` enforced | 300ms / 5s |
| `cache.invalidate.tag_fanout` | `cache` | `{tag, since}` | 07 | base 1s, max 5m, 5 attempts | UniqueKey `cache.invalidate.tag_fanout:{tag}` TTL 30s | 100ms / 1s |
| `cache.warmup` | `cache` | `{routes[]}` | 07 | base 5s, max 30m, 3 attempts | UniqueKey on route batch hash | 5s / 60s |
| `media.variant.generate` | `media` | `{media_id, variant_spec}` | 07 | base 10s, max 1h, 5 attempts | `(media_id, variant_spec_hash)`; compensating cleanup on retry (§5) | 800ms / 6s |
| `media.video.transcode` | `media` | `{media_id, profile, chunk_idx, chunk_count}` | 07 | base 30s, max 2h, 3 attempts | `(media_id, profile, chunk_idx)`; partial outputs cleaned on retry | 30s / 5m per chunk |
| `media.thumbnail.extract` | `media` | `{media_id, at_ms}` | 07 | base 10s, max 30m, 5 attempts | `(media_id, at_ms)` | 500ms / 3s |
| `media.cleanup.orphans` | `low` | `{since}` | 07 | base 5m, max 6h, 3 attempts | UniqueKey `media.cleanup.orphans:daily` | 30s / 5m |
| `media.cleanup.cold_variants` | `low` | `{cutoff_days}` | 07 | base 5m, max 6h, 3 attempts | UniqueKey daily | 60s / 10m |
| `revalidate.next` | `default` | `{paths[], tags[]}` | 07 | base 1s, max 10m, 8 attempts | UniqueKey per `(paths,tags)` hash TTL 10s | 50ms / 500ms |
| `migrate.batch.posts` | `migrate` | `{run_id, offset, limit}` | 08 | base 30s, max 2h, 5 attempts | `(run_id, offset, limit)` | 5s / 60s |
| `migrate.batch.media` | `migrate` | `{run_id, offset, limit}` | 08 | base 30s, max 2h, 5 attempts | `(run_id, offset, limit)` | 10s / 120s |
| `migrate.batch.users` | `migrate` | `{run_id, offset, limit}` | 08 | base 30s, max 2h, 5 attempts | `(run_id, offset, limit)` | 2s / 30s |
| `migrate.verify` | `migrate` | `{run_id, kind}` | 08 | base 60s, max 2h, 3 attempts | `(run_id, kind)` | 30s / 5m |
| `audit.archive` | `low` | `{cutoff}` | 06 | base 5m, max 6h, 3 attempts | UniqueKey `audit.archive:weekly` | 2m / 15m |
| `auth.session.cleanup` | `low` | `{cutoff}` | 06 | base 1m, max 1h, 3 attempts | UniqueKey hourly | 1s / 10s |
| `auth.token.cleanup` | `low` | `{cutoff}` | 06 | base 1m, max 1h, 3 attempts | UniqueKey hourly | 1s / 10s |
| `auth.brute_force.unlock` | `default` | `{principal, reason}` | 06 | base 30s, max 1h, 3 attempts | `(principal, reason)` | 50ms / 200ms |
| `search.reindex.post` | `default` | `{post_id}` | 01 | base 5s, max 30m, 5 attempts | `search.reindex.post:{id}` TTL 60s | 100ms / 1s |
| `search.reindex.term` | `default` | `{term_id}` | 01 | base 5s, max 30m, 5 attempts | `search.reindex.term:{id}` TTL 60s | 100ms / 1s |
| `search.reindex.comment` | `default` | `{comment_id}` | 01 | base 5s, max 30m, 5 attempts | `search.reindex.comment:{id}` TTL 60s | 100ms / 1s |
| `search.reindex.full` | `low` | `{kind}` | 01 | base 5m, max 6h, 3 attempts | UniqueKey `search.reindex.full:{kind}` for whole run | 5m / 60m |
| `revisions.purge` | `low` | `{policy_id, dry_run}` | 01 | base 5m, max 6h, 3 attempts | UniqueKey daily | 30s / 10m |
| `autosave.purge` | `low` | `{cutoff}` | 04 | base 5m, max 6h, 3 attempts | UniqueKey hourly | 5s / 60s |
| `rum.aggregate` | `low` | `{bucket_start, bucket_end}` | 10 | base 1m, max 1h, 3 attempts | `(bucket_start, bucket_end)` | 5s / 60s |
| `plugin.activate` | `plugins` | `{plugin_slug, version}` | 02 | base 5s, max 5m, 3 attempts | `(plugin_slug, version, "activate")` | 1s / 10s |
| `plugin.deactivate` | `plugins` | `{plugin_slug}` | 02 | base 5s, max 5m, 3 attempts | `(plugin_slug, "deactivate")` | 1s / 10s |
| `plugin.uninstall.cleanup` | `plugins` | `{plugin_slug, drain_queue}` | 02 | base 30s, max 1h, 5 attempts | `(plugin_slug, "uninstall")` | 5s / 60s |
| `plugin.cron.tick` | `plugins` | `{plugin_slug, task}` | 02 / 12 | per plugin's declared retry; default base 30s, max 1h, 3 attempts | UniqueKey `plugin.cron.tick:{slug}:{task}:{tick_ts}` | varies |
| `webhook.retry.dlq.scan` | `low` | `{}` | 12 | base 5m, max 1h, 3 attempts | UniqueKey 10-minute | 1s / 10s |

The catalog is also encoded in code (§3.1) so the registry is the source of truth, this table is documentation.

### 3.1 Registry pattern

```go
// tasktypes.go
package jobs

type TaskSpec struct {
    Type            string
    Queue           string
    MaxRetry        int
    Timeout         time.Duration
    RetryDelay      asynq.RetryDelayFunc
    UniqueTTL       time.Duration // 0 = no uniqueness
    Idempotent      bool          // safe to retry without compensation
    HumanName       string        // for admin UI
}

var registry = map[string]TaskSpec{
    "email.send": {
        Type: "email.send", Queue: "default",
        MaxRetry: 10, Timeout: 30 * time.Second,
        RetryDelay: exp(5*time.Second, time.Hour),
        Idempotent: true,
    },
    "media.video.transcode": {
        Type: "media.video.transcode", Queue: "media",
        MaxRetry: 3, Timeout: 10 * time.Minute,
        RetryDelay: exp(30*time.Second, 2*time.Hour),
        Idempotent: false, // partial outputs; handler must clean up
    },
    "search.reindex.full": {
        Type: "search.reindex.full", Queue: "low",
        MaxRetry: 3, Timeout: 2 * time.Hour,
        RetryDelay: exp(5*time.Minute, 6*time.Hour),
        UniqueTTL: 2 * time.Hour, // only one full reindex of a kind at a time
        Idempotent: true,
    },
    // ... rest of the catalog
}

func Spec(taskType string) (TaskSpec, bool) {
    s, ok := registry[taskType]
    return s, ok
}
```

Handlers must register against the same name. Enqueue API resolves the spec and applies its options unless the caller explicitly overrides them.

---

## 4. Plugin scoping

Plugins are untrusted (doc 02). They get jobs but on a leash.

### 4.1 Host ABI

Plugins enqueue via the host ABI:

```
queue.enqueue(task_type: string, payload: bytes, opts: EnqueueOpts) -> Result<TaskID, Error>
```

Capability required: `queue` (granted by manifest, like all capabilities in doc 02). Without it the host returns `EPERM`.

`EnqueueOpts` exposes only the safe subset of Asynq options: `delay`, `unique_for`, `max_retry` (capped at registry value), `idempotency_key`. Plugins **cannot** set the queue — the host always assigns `plugins`.

### 4.2 Per-plugin partition

Within the `plugins` queue, we partition by plugin slug. Implementation: the task type for plugin-enqueued work is rewritten to `plugin:{slug}:{user_task_type}`, and the queue is fixed to `plugins`. Asynq's flat queue model does not natively support sub-queues, but we get scoping via:

1. **Concurrency cap per plugin** — a Redis-backed in-process semaphore wrapping the handler. Default cap: 2 in-flight tasks per plugin per worker replica.
2. **Token-bucket rate limit per plugin per minute** — default 60 enqueues/min. Configurable per plugin via admin (super_admin only). Exceeded enqueues return `ERATELIMIT` to the plugin; the plugin can retry from its hook.

```go
// inside plugin handler dispatch
if !pluginRateLimiter.Allow(slug, 1) {
    return ErrRateLimited
}
sem := pluginSem(slug, cfg.PerPluginConcurrency)
if !sem.Acquire(ctx) { return ErrBusy }
defer sem.Release()
return dispatchPluginHook(ctx, slug, "job."+userType, payload)
```

### 4.3 Dispatch into WASM

Plugin jobs are not Go handlers. The Go-side handler for `plugin:{slug}:{user_task_type}` looks the plugin up, loads its WASM module (already cached in the plugin manager), and calls its registered hook handler `job.{user_task_type}` with the payload.

```go
func handlePluginTask(ctx context.Context, t *asynq.Task) error {
    slug, userType, err := parsePluginTaskType(t.Type())
    if err != nil { return asynq.SkipRetry }

    p, err := pluginMgr.Get(slug)
    if err != nil { return asynq.SkipRetry } // plugin gone
    if !p.Active { return asynq.SkipRetry }   // drained on deactivate

    inv := plugin.Invocation{
        Hook:    "job." + userType,
        Payload: t.Payload(),
        // Fuel + memory cap from manifest (doc 02)
        Fuel:    p.Manifest.Limits.JobFuel,
        Memory:  p.Manifest.Limits.JobMemoryMB,
        Timeout: p.Manifest.Limits.JobTimeout,
    }
    return p.Dispatch(ctx, inv)
}
```

Fuel/memory/timeout caps follow the same WASM sandbox rules as request handlers (doc 02), but with higher defaults (jobs are allowed to be slower than request hooks).

### 4.4 Lifecycle: deactivate, uninstall, version pin

- **Deactivate**: plugin marked inactive in DB; in-flight tasks are allowed to finish (handler check sees `Active=false` and returns `SkipRetry` if it has not yet started real work); queued tasks are **drained** (purged) for that slug.
- **Uninstall**: emits `plugin.uninstall.cleanup` which purges queued + scheduled + recurring tasks for the slug, then runs the plugin's `on_uninstall` hook one last time, then evicts the WASM module.
- **Version pin**: each enqueued plugin task carries the plugin version in payload. On dispatch, if the active version differs, the handler returns `SkipRetry` (the task is stale).

```go
type PluginTaskPayload struct {
    Slug      string          `json:"slug"`
    Version   string          `json:"version"`
    UserType  string          `json:"user_type"`
    Body      json.RawMessage `json:"body"`
    EnqueuedBy string         `json:"enqueued_by"` // user_id or "system"
}
```

### 4.5 Plugin-defined cron

See §8.3.

---

## 5. Retry policy

### 5.1 Default schedule

Exponential backoff with full jitter:

```
delay(attempt) = jitter(min(base * 2^(attempt-1), max))
```

Defaults: `base = 1s`, `max = 1h`, `attempts = 25`. Overridden per task type from the registry. Most production task types cap attempts much lower (3–10) because if a job has failed ten times over an hour, an automated 25th retry is not the right move — the DLQ is.

```go
func exp(base, max time.Duration) asynq.RetryDelayFunc {
    return func(n int, err error, t *asynq.Task) time.Duration {
        d := base << min(n, 20) // cap shift to avoid overflow
        if d > max { d = max }
        // full jitter
        return time.Duration(rand.Int63n(int64(d)))
    }
}
```

### 5.2 Idempotent vs non-idempotent

**Idempotent (safe to retry as-is):**
- `email.send` (with idempotency key, downstream provider dedupes)
- `webhook.deliver` (subscriber is expected to dedupe by event_id; our HMAC includes it)
- `cache.invalidate.*`, `revalidate.next`, `search.reindex.*`
- `audit.archive`, `auth.*.cleanup`, `revisions.purge`, `autosave.purge`

**Non-idempotent (require compensating cleanup before retry):**
- `media.variant.generate` — may have written a partial WebP/AVIF to S3. Handler's first step on retry: `DELETE s3://variants/{media_id}/{variant_hash}.tmp*`.
- `media.video.transcode` — same, plus may have transcoded N of M segments. Each chunk task is itself idempotent at the chunk level; chunking turns the non-idempotent whole into idempotent pieces (§9).
- `migrate.batch.*` — handler reads checkpoint from DB at start; if checkpoint says "finished" the task is a no-op. Otherwise rolls back partial writes via the migration's transaction.

A non-idempotent task type is required to declare its compensation in the handler. Doc 07 owns media compensation specifics; doc 08 owns migration compensation.

### 5.3 Errors that skip retry

A handler returning `asynq.SkipRetry` (wrap with `fmt.Errorf("…: %w", asynq.SkipRetry)`) sends the task directly to the archive. Use for:

- 4xx-class problems we cannot resolve by retrying (e.g., webhook target permanently 410).
- Plugin gone / deactivated / version mismatch (§4.4).
- Malformed payload (we never wrote it; do not loop on poison).

A handler returning `nil` is success. Any other error triggers a retry per policy.

### 5.4 Decision tree per task type

```
                  task fails
                      │
       ┌──────────────┼──────────────┐
       ▼              ▼              ▼
  SkipRetry?     transient err   permanent err
  (4xx,           (timeout,      (poison payload,
   gone,           5xx, IO)       schema mismatch)
   stale)
       │              │              │
       ▼              ▼              ▼
   archived       retry per       archived
   immediately    policy          immediately
                      │
                  attempts
                  exhausted?
                      │
                ┌─────┴─────┐
                ▼           ▼
              yes          no
                │           │
                ▼           ▼
            archived     wait
            (DLQ)        backoff
```

---

## 6. Dead-letter queue

When a task exhausts retries (or returns `SkipRetry`), Asynq moves it to the **archive** state. This is our DLQ.

### 6.1 Surface

Admin UI under **System → Jobs → Failed** (doc 05). Columns: task type, queue, last error, attempts, archived_at, payload preview. Row actions:

- **Replay** — re-enqueue with attempt count reset. Requires capability `manage_jobs` (super_admin by default).
- **Discard** — permanently delete.
- **Mark "investigate"** — tag the task to exempt it from auto-purge.
- **Inspect payload** — full JSON view. Payload is **redacted** for tasks declared sensitive: `email.password_reset`, `email.verify` strip the token_ref; `webhook.deliver` strips the body_ref (admin can fetch separately with audit log entry).

### 6.2 Retention

- Default: 14 days, then auto-purged by `webhook.retry.dlq.scan` running every 10 minutes (despite the name, this scanner handles all DLQ retention, not just webhooks — it predated the broader use).
- Tasks tagged "investigate" are exempt.
- Hard cap: 100k archived tasks at any time. Above the cap we purge oldest non-tagged first to make room. Cap configurable.

### 6.3 Metrics & alerts

(Forward-ref doc 10 §3 for the metrics catalog.)

- `jobs_dlq_size{queue=…}` gauge
- `jobs_dlq_growth_rate{queue=…}` derived
- Alert: DLQ size > 1000 OR growth > 100/min sustained 5min → page

---

## 7. Idempotency

### 7.1 The recommended pattern

Every task that mutates state carries an `idem_key` field in its payload. The handler's first action is:

```go
func handle(ctx context.Context, t *asynq.Task) error {
    var p Payload
    if err := json.Unmarshal(t.Payload(), &p); err != nil {
        return fmt.Errorf("decode: %w", asynq.SkipRetry)
    }
    if p.IdemKey != "" {
        ok, err := idemStore.Claim(ctx, t.Type(), p.IdemKey, 24*time.Hour)
        if err != nil { return err }
        if !ok { return nil } // already done; success
    }
    // ... do work ...
    return nil
}
```

`idemStore.Claim` is `SET NX EX` against Redis with key `idem:{task_type}:{idem_key}`. The TTL bounds memory; the value is the worker_id + completed_at for debuggability.

### 7.2 Per-task idempotency-key conventions

| Task | Idempotency-key formula |
|---|---|
| `email.send` | `sha256(template + ":" + to + ":" + content_hash)`, TTL 24h |
| `email.password_reset` | `(user_id, token_ref)`, TTL 1h (token lifetime) |
| `webhook.deliver` | `(subscription_id, event_id)`, TTL 7 days |
| `revalidate.next` | `sha256(sorted(paths+tags))`, TTL 10s (same fanout within 10s is wasteful) |
| `search.reindex.post` | `post_id`, TTL 60s |
| `migrate.batch.posts` | `(run_id, offset, limit)`, TTL 30 days |

### 7.3 In-flight dedup vs persistent guarantees

Two layers:

1. **In-flight dedup** — Redis `SET NX` with short TTL prevents two concurrent handlers from doing the same work. Sufficient for cache/revalidate/reindex.
2. **Persistent dedup** — for tasks where the same idem_key must not be re-executed even after the in-flight TTL expires (e.g., webhook delivery for the same event), we maintain a `task_idempotency` table:
   ```sql
   CREATE TABLE task_idempotency (
       task_type   TEXT NOT NULL,
       idem_key    TEXT NOT NULL,
       completed_at TIMESTAMPTZ NOT NULL,
       result_hash TEXT,
       PRIMARY KEY (task_type, idem_key)
   );
   ```
   The handler writes to this table inside the same DB transaction as its side effect (where one exists). On retry, the table check is the first thing — already there means already done.

Use the Redis layer for tasks that are cheap to redo if dedup ever lapses; use the persistent layer for "must not fire twice ever" semantics like webhooks and emails to external recipients.

---

## 8. Cron

### 8.1 Leader election

Exactly one cron dispatcher must run across all worker replicas. Asynq's `PeriodicTaskManager` + `Scheduler` is built for this and uses a Redis lease.

**Pattern:**

- Every replica starts a `Scheduler` instance.
- Each scheduler attempts to acquire the lease key `asynq:scheduler:lease` via `SET NX EX 30s`.
- The holder enqueues scheduled tasks until its lease expires; it refreshes every 10s.
- If the leader crashes, its lease expires within 30s and the next replica to try `SET NX` wins. New leader picks up from the next scheduled tick (we may miss a tick during the gap — acceptable for our cron cadence; see §8.4 for catch-up).
- Lease holder is logged so operators can see who is leading.

```go
sched := asynq.NewScheduler(redisOpt, &asynq.SchedulerOpts{
    Location:        time.UTC,
    EnqueueErrorHandler: enqueueErrLogger,
    PreEnqueueFunc:  preEnqueue,
    PostEnqueueFunc: postEnqueue,
})
// Start blocks until shutdown; internally manages the lease.
go func() {
    if err := sched.Run(); err != nil { log.Fatal(err) }
}()
```

We wrap this in `cron.LeaderDispatcher` that also registers our cron specs at boot and dynamically re-registers when a plugin is activated/deactivated.

### 8.2 Core cron jobs

| Task | Schedule | Queue | Notes |
|---|---|---|---|
| `revisions.purge` | `@daily 03:00` | `low` | Honors per-post-type retention policy from doc 01. |
| `autosave.purge` | `@hourly` | `low` | Drops autosaves older than 7 days. |
| `media.cleanup.orphans` | `@daily 03:30` | `low` | S3 objects with no DB ref. |
| `media.cleanup.cold_variants` | `@weekly Sun 04:00` | `low` | Drop variants with 0 hits in 60 days. |
| `auth.session.cleanup` | `@hourly` | `low` | Expired sessions. |
| `auth.token.cleanup` | `@hourly` | `low` | Expired tokens. |
| `audit.archive` | `@weekly Sun 02:00` | `low` | Move audit rows older than 90d to cold storage. |
| `search.reindex.full` | `@daily 02:00` | `low` | Nightly delta full reindex; manual full also available. |
| `rum.aggregate` | `*/5 * * * *` | `low` | 5-minute buckets. |
| `webhook.retry.dlq.scan` | `*/10 * * * *` | `low` | DLQ retention + alert. |
| `cache.warmup` | `@daily 05:00` | `cache` | Warm top 200 routes. |

All cron-dispatched tasks are themselves regular tasks subject to the retry and idempotency rules in §5–7. The cron just enqueues them on schedule.

### 8.3 Plugin cron

Plugins declare cron schedules in their manifest (doc 02):

```json
{
  "cron": [
    {"task": "sitemap.refresh", "schedule": "@hourly", "queue": "plugins"},
    {"task": "stats.rollup",    "schedule": "0 4 * * *"}
  ]
}
```

At plugin activation, the cron loader registers each entry with our `LeaderDispatcher`. The resulting Asynq scheduled task type is `plugin:{slug}:{user_task}` and lands on the `plugins` queue (see §4.2).

**Rules:**

- Minimum schedule cadence is **one minute** (`* * * * *`). Schedules more frequent (which Asynq cron parser does not support anyway) are rejected at manifest validation.
- Plugin cron ticks count against the plugin's per-minute enqueue rate-limit (§4.2). A `@minute` plugin cron eats 1 of its 60 tokens.
- Admins can **disable a plugin's cron** from the admin UI without uninstalling: a per-plugin flag `cron_enabled` gates the cron loader on activation and a runtime cron-tick handler that returns `SkipRetry` if disabled.
- Plugin cron uses the same `UniqueKey` for the tick id so duplicate enqueues (rare: two leaders during a lease transition) do not double-fire.

### 8.4 Missed-tick policy

If the scheduler is down longer than a tick's interval, Asynq does not retroactively fire missed ticks. We accept this for our cron set (none are sub-minute; missing one nightly purge is fine — the next night picks up the same backlog). For tasks that must catch up:

- `audit.archive`, `revisions.purge`, `media.cleanup.*` are written so a single run handles arbitrary backlog (they query "everything older than cutoff," not "everything since last run").
- `rum.aggregate` re-aggregates by `(bucket_start, bucket_end)` so a late run for bucket B is fine — its idem key prevents double-aggregation.

If we ever add a cron task that requires strict tick-by-tick semantics, we will add a "missed ticks" replay step in the scheduler that compares last-success timestamp to now and fires the gap.

---

## 9. Job-level timeouts & resource limits

### 9.1 Wall-clock timeouts

Every task has a `Timeout` in its registry spec. We pass `asynq.Timeout(spec.Timeout)` at enqueue. The handler context expires at that point; handlers must respect context cancellation.

Defaults: 30s (most tasks), 5–10m (migrate batches, video chunks), 2h (full reindex, audit archive). No task type's default timeout is unbounded.

### 9.2 Plugin job limits

Plugin job invocations carry the plugin's WASM fuel/memory caps from manifest. Defaults (from doc 02 for jobs specifically):

- `job_fuel`: 10M instructions (10× a request hook)
- `job_memory_mb`: 64 MB
- `job_timeout`: 30s

Exceeding fuel or timeout cancels the WASM execution; the host returns an error and Asynq applies retry policy. Plugins that need more must request `high_resource_jobs` capability at install time (admin grants explicitly).

### 9.3 Long-running tasks split into chunks

**Anti-pattern**: a single handler that runs for 30 minutes. Reasons:

- A worker restart in the middle loses the work.
- Asynq cannot show progress.
- One bad task starves a concurrency slot.

**Pattern**: split into a coordinator + chunk tasks.

```go
// Coordinator: media.video.transcode (the originally enqueued one)
func handleVideoCoord(ctx context.Context, t *asynq.Task) error {
    p := parse(t)
    chunks := planChunks(p.MediaID, p.Profile) // probe ffmpeg, decide N chunks
    for i, c := range chunks {
        client.Enqueue(asynq.NewTask("media.video.transcode.chunk", marshal(c)),
            asynq.Queue("media"), asynq.Unique(2*time.Hour))
        _ = i
    }
    // Enqueue a finalizer that waits for all chunks
    client.Enqueue(asynq.NewTask("media.video.transcode.finalize",
        marshal(p)), asynq.Queue("media"),
        asynq.ProcessIn(5*time.Minute)) // poll-based finalize
    return nil
}
```

Each chunk is a few seconds to a few minutes. The finalize task either combines chunks or re-schedules itself if not all chunks are done. Doc 07 owns the chunk-spec details.

Same pattern for `migrate.batch.*`: doc 08's migration coordinator enqueues N batch tasks, each handling a bounded `(offset, limit)`.

---

## 10. Concurrency control

### 10.1 Per-queue concurrency

Set globally per worker replica via the queue→concurrency CSV (§2.2). Asynq enforces these as a weighted pool.

### 10.2 Per-key uniqueness

For tasks where exactly one in-flight is meaningful, use Asynq's UniqueKey:

```go
client.Enqueue(
    asynq.NewTask("search.reindex.full", payload),
    asynq.Queue("low"),
    asynq.Unique(2*time.Hour),
)
```

`asynq.Unique(ttl)` builds a Redis key derived from `(queue, type, payload)`. While the key is set, duplicate enqueues fail with `asynq.ErrDuplicateTask`. We catch and treat as "ok, already queued."

Canonical patterns:

| Pattern | Key derivation | TTL |
|---|---|---|
| **One full run at a time** | `(type, kind)` — e.g., `search.reindex.full:posts` | run duration + buffer (2h) |
| **Coalesce fanouts** | `(type, target_hash)` — `cache.invalidate.tag_fanout:home` | short (10–30s) |
| **One per resource** | `(type, resource_id)` — `search.reindex.post:42` | < expected processing time |
| **Per-bucket aggregation** | `(type, bucket_start)` — `rum.aggregate:2026-05-13T10:00` | bucket length + grace |

For custom keys (where the payload alone is wrong, e.g., we want to dedupe on a single field), we set `UniqueKey` explicitly in the enqueue helper:

```go
func enqueueReindexPost(postID int64) error {
    _, err := client.Enqueue(
        asynq.NewTask("search.reindex.post", marshal(postID)),
        asynq.Queue("default"),
        asynq.TaskID(fmt.Sprintf("search.reindex.post:%d", postID)),
        asynq.Unique(60*time.Second),
    )
    if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
        return nil
    }
    return err
}
```

### 10.3 Per-plugin concurrency

Already covered in §4.2.

---

## 11. Observability

(Forward-ref doc 10 §3 for the canonical metrics catalog and dashboards. This section names what we emit.)

### 11.1 Structured logs

Each task emits, with a shared `task_id`, `task_type`, `queue`, `attempt`:

- `task.enqueued` — at enqueue site (with caller info)
- `task.started` — at handler entry
- `task.completed` — at handler success exit (with duration)
- `task.failed` — at handler error exit (with error, will_retry, next_retry_at)
- `task.archived` — when a task is moved to DLQ
- `task.replayed` — admin-triggered replay

### 11.2 Tracing

A trace context is propagated through enqueue: the enqueuer's span ID is stored in the task payload's `_trace` field (separate from the user payload). The handler starts a span with that as the parent so the trace shows enqueue → run as a single graph.

### 11.3 Metrics

- `jobs_enqueued_total{type,queue}`
- `jobs_processed_total{type,queue,result=success|fail}`
- `jobs_duration_seconds{type,queue}` histogram, p50/p95/p99
- `jobs_queue_depth{queue}` gauge (pending)
- `jobs_queue_active{queue}` gauge (in-flight)
- `jobs_retries_total{type,queue}`
- `jobs_dlq_size{queue}` gauge
- `jobs_idempotency_skips_total{type}` (claimed = already done)
- `jobs_unique_conflicts_total{type}` (enqueue suppressed)
- `cron_lease_holder` (info-style gauge with replica label)

### 11.4 Admin UI

System → Jobs has tabs:

- **Overview**: per-queue depth + processing rate + p50/p95 + failure rate.
- **Active**: in-flight tasks (Asynq inspector).
- **Failed (DLQ)**: §6.
- **Scheduled**: cron + delayed.
- **History**: recent completed (sampled).

Most of this is Asynq's built-in inspector data; we wrap it in our admin auth.

---

## 12. Backpressure

When a queue's pending depth exceeds a configured threshold, the enqueue API begins to shed non-critical tasks.

### 12.1 Shed order

1. `low` — sheds first at depth > 5,000.
2. `migrate` — sheds at depth > 2,000.
3. `plugins` — sheds at depth > 2,000.
4. `cache` — sheds at depth > 10,000 (high — we expect bursts).
5. `media` — sheds at depth > 1,000.
6. `default` — sheds at depth > 5,000 (high — webhooks can burst).
7. `critical` — **never sheds**. We will OOM before dropping a password reset.

### 12.2 Shed behavior

- For HTTP-originated enqueues, the API returns **503** with `Retry-After: 30` and a JSON body explaining the queue is saturated.
- For internal callers (e.g., post-publish fanout), the enqueue helper returns `ErrBackpressure`; callers in the hot path swallow it for fire-and-forget work (cache invalidation fanout — we will revalidate later anyway).
- Webhook deliveries shed first within `default`: the `webhook.deliver` enqueue path checks `default` depth and returns `ErrBackpressure` independently. Other `default` tasks (transactional email) continue until the queue-wide threshold.
- Sheds are counted (`jobs_shed_total{queue}`) and alert at sustained rate.

### 12.3 Threshold tuning

Thresholds are config (`config.yaml: jobs.backpressure.<queue>`). The defaults above are starting points; operators tune per traffic shape.

---

## 13. Plugin cron security

Recap of constraints (see §4.5, §8.3):

- Plugin cron task ticks **count against the plugin's per-minute enqueue rate limit**. A plugin with a `@minute` cron and 60/min limit has 59 enqueues/minute left for everything else.
- **Minimum cadence: one minute.** Manifest validation rejects sub-minute schedules. Asynq cron does not support them anyway.
- **Admin disable**: a `cron_enabled=false` flag per plugin in the admin gates cron loading at activation. Toggling at runtime takes effect within one minute (the LeaderDispatcher re-syncs cron registrations every minute against the active plugin set).
- **Capability**: plugin cron requires the `queue` capability and an explicit `cron` array in the manifest. No capability, no cron.
- **Audit**: every plugin cron registration is logged to the audit trail (doc 06).

---

## 14. Webhook delivery

Doc 05 owns the **subscription/event surface**: who can subscribe, the events catalog, signing-secret management, the admin UI for managing subscriptions. **Doc 12 owns the delivery mechanics:** the `webhook.deliver` task definition, the retry schedule, HMAC signing on each attempt, and DLQ behavior.

### 14.1 Task definition

```go
type WebhookDeliverPayload struct {
    SubscriptionID string `json:"subscription_id"`
    EventID        string `json:"event_id"`
    URL            string `json:"url"`
    BodyRef        string `json:"body_ref"`        // ref into Redis or object store (large bodies)
    HMACSecretRef  string `json:"hmac_secret_ref"` // ref into secrets store; not the raw secret
    Headers        map[string]string `json:"headers,omitempty"`
}
```

Body and secret are stored separately from the payload to keep the queue small and to keep secrets out of DLQ JSON dumps.

### 14.2 Retry schedule

Custom (not the default exp) — front-loaded for transient blips, then longer for legitimate downtime:

| Attempt | Delay |
|---|---|
| 1 | (immediate) |
| 2 | 1s + jitter |
| 3 | 5s + jitter |
| 4 | 30s + jitter |
| 5 | 2m + jitter |
| 6 | 10m + jitter |
| 7 | 1h + jitter |
| 8 | 6h + jitter |
| — | archive |

`RetryDelayFunc` for `webhook.deliver` returns from a fixed slice.

### 14.3 HMAC signing

Every attempt (including retries) signs with:

```
sig = HMAC-SHA256(secret, timestamp + "." + body)
header X-GoNext-Signature: t=<unix>,v1=<hex(sig)>
header X-GoNext-Event-Id: <event_id>
header X-GoNext-Delivery-Id: <task_id>
```

The signature is recomputed each attempt (the timestamp is fresh). The event_id stays constant across attempts so subscribers can dedupe.

### 14.4 Success criteria

HTTP 2xx within configured timeout (default 10s) = success. Any other response or transport error = retryable, unless:

- **410 Gone** → `SkipRetry`, archive immediately, and emit `webhook.subscription.dead` event for the admin.
- **400/422 with `X-GoNext-Reject: permanent`** header → `SkipRetry`, archive.
- **429** with `Retry-After` → respect the header (delay at least the larger of next-step and `Retry-After`).

### 14.5 DLQ surfacing

Failed webhook deliveries appear in admin's webhook-specific failure view (under the subscription) and in the global DLQ view. Replay is available; "discard all for subscription" is available; "circuit-break this subscription" is also a one-click action (sets the subscription to paused, drains queued deliveries).

---

## 15. Operational tasks (CLI)

`gonext jobs …`:

| Command | What |
|---|---|
| `gonext jobs queue stats` | Print depth/active/processed/failed per queue. |
| `gonext jobs queue list` | List configured queues + weights. |
| `gonext jobs failed list [--task=X] [--queue=Y] [--limit=100]` | List archived tasks. |
| `gonext jobs failed show <id>` | Print full record (payload, error history). |
| `gonext jobs failed replay <id>` | Re-enqueue one task. |
| `gonext jobs failed replay-all --task=X` | Replay all archived of a type. Guarded by confirmation. |
| `gonext jobs failed discard <id>` | Delete from archive. |
| `gonext jobs drain --queue=migrate` | Pause processing (enqueue continues; workers stop pulling). |
| `gonext jobs resume --queue=migrate` | Resume. |
| `gonext jobs cron list` | Show all registered cron specs + next-run. |
| `gonext jobs cron lease` | Show current scheduler lease holder. |
| `gonext jobs plugin rate-limit set --slug=X --per-min=N` | Adjust per-plugin rate. |
| `gonext jobs plugin drain --slug=X` | Drain a plugin's queued + scheduled tasks. |

All commands talk to Redis directly via Asynq's `Inspector` API. `drain` uses Asynq's pause-queue feature.

---

## 16. Migrations from in-process to job

**Pattern**: when a request handler can defer work, prefer enqueue + return over inline execution.

### 16.1 Things that should be jobs (not inline)

| Action | Why |
|---|---|
| ISR revalidate fanout after publish | Touches up to dozens of routes; never block the publish response. |
| Webhook delivery | Network out to third parties; subscribers can be slow. |
| Email send | SMTP/API may stall; never block a signup. |
| Audit row archive | I/O heavy; not the user's problem. |
| Search index update on save | Postgres FTS update is fast but cascades (post + terms + comments); enqueue per shard. |
| Media variant generation on upload | Originals upload completes; variants happen in the background. |
| Cache tag fan-out invalidation | Hundreds of keys; do it async. |

### 16.2 Anti-pattern: enqueue-and-poll-in-request

```go
// BAD
id, _ := client.Enqueue(t)
for {
    r, _ := inspector.GetTaskInfo(q, id)
    if r.State == "completed" { break }
    time.Sleep(50*time.Millisecond)
}
respond(r.Result)
```

If you need the result inside the request, just do the work inline. The queue is for fire-and-forget; round-tripping through Redis for in-request latency is strictly worse than a direct function call.

### 16.3 When inline beats enqueue

- The work is < 50ms p99 and the user genuinely needs the side effect to be visible in the next request. (Example: stamping `updated_at` is a DB write, not a job.)
- The work is part of the request's transaction. (Example: writing the post row; you do not enqueue an "insert post" task.)
- The work is read-mostly. (Caches hot in process.)

Rule of thumb: **if losing this work to a worker crash would not visibly hurt the user, it belongs in the queue.**

---

## 17. Code sketches

### 17.1 Handler registration

```go
package jobs

import (
    "context"
    "github.com/hibiken/asynq"
)

type Handlers struct {
    Email   *email.Service
    Media   *media.Service
    Cache   *cache.Service
    Plugin  *plugin.Manager
    DB      *sql.DB
    // ...
}

func (h *Handlers) Register(mux *asynq.ServeMux) {
    // Each handler wrapped with: idempotency check, telemetry, plugin scoping.
    mux.HandleFunc("email.send",                    h.handleEmailSend)
    mux.HandleFunc("email.password_reset",          h.handleEmailPasswordReset)
    mux.HandleFunc("webhook.deliver",               h.handleWebhookDeliver)
    mux.HandleFunc("cache.invalidate.tag_fanout",   h.handleCacheInvalidateTagFanout)
    mux.HandleFunc("revalidate.next",               h.handleRevalidateNext)
    mux.HandleFunc("media.variant.generate",        h.handleMediaVariantGenerate)
    mux.HandleFunc("media.video.transcode",         h.handleVideoCoord)
    mux.HandleFunc("media.video.transcode.chunk",   h.handleVideoChunk)
    mux.HandleFunc("media.video.transcode.finalize",h.handleVideoFinalize)
    mux.HandleFunc("migrate.batch.posts",           h.handleMigratePosts)
    mux.HandleFunc("audit.archive",                 h.handleAuditArchive)
    mux.HandleFunc("search.reindex.post",           h.handleSearchReindexPost)
    mux.HandleFunc("search.reindex.full",           h.handleSearchReindexFull)
    mux.HandleFunc("revisions.purge",               h.handleRevisionsPurge)
    mux.HandleFunc("autosave.purge",                h.handleAutosavePurge)
    mux.HandleFunc("rum.aggregate",                 h.handleRUMAggregate)
    mux.HandleFunc("auth.session.cleanup",          h.handleAuthSessionCleanup)
    mux.HandleFunc("plugin.activate",               h.handlePluginActivate)
    mux.HandleFunc("plugin.deactivate",             h.handlePluginDeactivate)
    mux.HandleFunc("plugin.uninstall.cleanup",      h.handlePluginUninstallCleanup)

    // Wildcard for plugin user tasks: registered last.
    mux.HandleFunc("plugin:", h.handlePluginTask)
}

func Boot(cfg Config, h *Handlers) (*asynq.Server, error) {
    srv := asynq.NewServer(
        asynq.RedisClientOpt{Addr: cfg.RedisAddr},
        asynq.Config{
            Concurrency: cfg.Concurrency,
            Queues:      cfg.QueueWeights,
            ErrorHandler: asynq.ErrorHandlerFunc(errLogger(h)),
            RetryDelayFunc: defaultRetryDelay, // overridden per-type via spec
            IsFailure: isFailure,
        },
    )
    mux := asynq.NewServeMux()
    mux.Use(loggingMiddleware, tracingMiddleware, panicRecoveryMiddleware)
    h.Register(mux)
    return srv, srv.Run(mux)
}
```

### 17.2 An idempotent handler

```go
func (h *Handlers) handleEmailSend(ctx context.Context, t *asynq.Task) error {
    var p EmailSendPayload
    if err := json.Unmarshal(t.Payload(), &p); err != nil {
        return fmt.Errorf("decode: %w", asynq.SkipRetry)
    }

    // 1. Idempotency claim (Redis SET NX).
    if p.IdemKey != "" {
        claimed, err := h.Idem.Claim(ctx, "email.send", p.IdemKey, 24*time.Hour)
        if err != nil { return err }
        if !claimed {
            metrics.IdemSkips.WithLabelValues("email.send").Inc()
            return nil
        }
    }

    // 2. Render.
    body, subj, err := h.Email.Render(p.Template, p.Locale, p.Vars)
    if err != nil { return fmt.Errorf("render: %w", asynq.SkipRetry) }

    // 3. Send via provider; provider also dedupes on idem_key (belt + suspenders).
    if err := h.Email.Provider.Send(ctx, p.To, subj, body, p.IdemKey); err != nil {
        return err // retryable
    }
    return nil
}
```

### 17.3 A unique-key fanout

```go
func enqueueRevalidateNext(client *asynq.Client, paths, tags []string) error {
    p := RevalidatePayload{Paths: paths, Tags: tags}
    body, _ := json.Marshal(p)
    key := sha256Hex(append(sortJoin(paths), sortJoin(tags)...))
    _, err := client.Enqueue(
        asynq.NewTask("revalidate.next", body),
        asynq.Queue("default"),
        asynq.TaskID("revalidate.next:"+key),
        asynq.Unique(10*time.Second),
        asynq.Timeout(30*time.Second),
        asynq.MaxRetry(8),
    )
    if errors.Is(err, asynq.ErrTaskIDConflict) ||
       errors.Is(err, asynq.ErrDuplicateTask) {
        metrics.UniqueConflicts.WithLabelValues("revalidate.next").Inc()
        return nil
    }
    return err
}
```

### 17.4 Plugin job dispatch

```go
func (h *Handlers) handlePluginTask(ctx context.Context, t *asynq.Task) error {
    slug, userType, ok := parsePluginTaskType(t.Type())
    if !ok { return fmt.Errorf("bad plugin task type: %w", asynq.SkipRetry) }

    var p PluginTaskPayload
    if err := json.Unmarshal(t.Payload(), &p); err != nil {
        return fmt.Errorf("decode: %w", asynq.SkipRetry)
    }

    pl, err := h.Plugin.Get(slug)
    if err != nil { return fmt.Errorf("plugin gone: %w", asynq.SkipRetry) }
    if !pl.Active { return fmt.Errorf("inactive: %w", asynq.SkipRetry) }
    if pl.Version != p.Version {
        return fmt.Errorf("version mismatch: %w", asynq.SkipRetry)
    }

    // Per-plugin concurrency.
    sem := h.Plugin.Sem(slug)
    if !sem.AcquireCtx(ctx) { return errors.New("plugin sem busy: will retry") }
    defer sem.Release()

    inv := plugin.Invocation{
        Hook:    "job." + userType,
        Payload: p.Body,
        Fuel:    pl.Manifest.Limits.JobFuel,
        Memory:  pl.Manifest.Limits.JobMemoryMB,
        Timeout: pl.Manifest.Limits.JobTimeout,
    }
    return pl.Dispatch(ctx, inv)
}
```

---

## 18. Dispatch flow (ASCII)

```
   ┌──────────────────────────┐
   │ HTTP handler / hook      │
   │  (post.publish, upload,  │
   │   plugin queue.enqueue)  │
   └─────────────┬────────────┘
                 │ Enqueue(task, opts)
                 ▼
   ┌──────────────────────────┐
   │ Enqueue middleware       │
   │  - resolve TaskSpec      │
   │  - apply timeout/retry   │
   │  - inject _trace, idem   │
   │  - backpressure check    │
   │  - per-plugin rate-limit │
   └─────────────┬────────────┘
                 │ EnqueueTask
                 ▼
        ┌───────────────────┐
        │   Redis (Asynq)   │
        │                   │
        │  ┌─────────────┐  │       ┌───────────────────────────┐
        │  │ pending     │◀─┼───────│  Scheduler (LEADER)        │
        │  │ scheduled   │  │       │   cron specs + leases      │
        │  │ retry       │  │       │   ┌──────────────────────┐ │
        │  │ active      │  │  Redis│   │ SET NX EX 30s         │ │
        │  │ archived    │  │  lease│   │ refresh every 10s     │ │
        │  └─────────────┘  │ ◀─────│   └──────────────────────┘ │
        │   per-queue       │       └───────────────────────────┘
        │   priorities      │
        └─────────┬─────────┘
                  │ BRPOPLPUSH (Asynq internals)
                  ▼
        ┌────────────────────────────────────────────────┐
        │ Worker replicas (asynq.Server x N)            │
        │                                               │
        │  ┌────────────────┐   ┌────────────────┐      │
        │  │ Handler mux    │   │ Middleware:    │      │
        │  │  email.send    │ ← │  log, trace,    │      │
        │  │  webhook.del   │   │  panic recover, │      │
        │  │  media.variant │   │  idem claim     │      │
        │  │  plugin:* ─────┼─▶ plugin WASM      │      │
        │  └────────────────┘   └────────────────┘      │
        │           │                                   │
        │     success │  fail (retry)  │  SkipRetry     │
        │           ▼                  ▼                │
        │  ✔ completed       requeue (backoff)          │
        │                    OR move to archive (DLQ)   │
        └────────────────────────────────────────────────┘
                          │
                          ▼
                ┌──────────────────┐
                │ Admin UI:        │
                │  System → Jobs   │
                │  - depth/rate    │
                │  - DLQ replay    │
                │  - cron leader   │
                └──────────────────┘

   Scheduler leader transition (lease expiry):

      Replica A          Replica B          Replica C
        │                  │                  │
   ╔═══ holds lease ═══════════════════╗      │
        │                  │                  │
        │ (crash)          │                  │
        ✖                  │                  │
                           │                  │
                       (≤30s gap)             │
                           │                  │
                           ╔══ holds lease ═══════
                           │                  │
                       resumes cron enqueues  │
```

---

## 19. Trade-offs & rejected alternatives

### 19.1 Redis as the queue store

Pros: blazing fast, we already run it, Asynq is purpose-built for it. Cons: Redis is not durable by default — a crash can lose seconds of accepted-but-unwritten enqueues. We mitigate with:

- AOF persistence (`appendfsync everysec`) on the Redis used for jobs (separate logical Redis or DB index from cache).
- Critical (`critical` queue) tasks also write a row to a `pending_critical_tasks` table inside the same DB transaction that caused them, and a periodic reconciler re-enqueues anything in that table not yet completed. Acceptable for password-reset volume; not used for `default`.

A future migration to River (Postgres-backed) would eliminate this; not worth it for v1.

### 19.2 Temporal

Already covered (§1.1). One more note: Temporal's workflow programming model is wonderful for long-running, multi-step orchestration. Almost nothing in this CMS is that. Our "long" jobs (migration, video transcode) are coordinator + chunks, which we model trivially in Asynq.

### 19.3 River

Postgres-backed. Compelling because it removes a class of Redis-durability concerns and gives transactional enqueue (insert post + enqueue revalidate in one TX). But:

- River is newer; we want a CMS-grade backbone that has run in production for years.
- It does not yet have leader-elected cron and unique-task ergonomics matching Asynq.
- We already need Redis for sessions and cache; the "remove Redis" argument doesn't apply.

Concrete v2 trigger to revisit: if Redis durability bites us in practice, or we want transactional enqueue badly enough.

### 19.4 In-process goroutine workers

Already covered. Tempting "v0" — explicitly rejected because there is no migration story from it to real queues without rewriting every call site. We pay the small cost of Asynq from day one.

### 19.5 RabbitMQ / NATS JetStream

Message brokers, not queues. Building cron, retries, DLQ, unique-tasks on top is a year of yak shaving.

### 19.6 Per-tenant queues

Tempting: each tenant gets `default:tenant:{id}`. Rejected for v1 because:

- We are single-tenant in v1 (doc 00 §7 open question: multisite is v2 or later).
- Asynq's per-queue weight has limits; thousands of queues is not the design intent.
- Per-tenant fairness is better solved by per-tenant rate limits at enqueue + per-tenant accounting in metrics.

### 19.7 Priority preemption

We do not preempt running tasks. If a `low` task is mid-flight when a `critical` arrives, the `critical` waits for a free slot. Acceptable because `critical` concurrency (4) is sized assuming there are always free slots; tasks on `critical` are tiny.

True priority preemption (kill a low to free a slot for critical) is hard to do correctly in Go without cooperative cancellation in the handler, and even harder for plugin WASM. Rejected.

### 19.8 Polling vs queue for "wait for completion"

We considered exposing a "wait for job" RPC. Rejected: it encourages the enqueue-and-poll anti-pattern (§16.2). For results that the user truly needs, the originating endpoint should accept a callback URL / webhook subscription or stream via SSE.

---

## 20. Open questions

1. **Cross-region cron.** Today the scheduler lease is one Redis. In a multi-region deployment with a primary Redis, the scheduler is single-region. Acceptable for v1; v2 should consider a leader election that survives a region outage (e.g., dual-write the lease to two Redis or move cron to a dedicated, multi-region-aware coordination service).
2. **Per-tenant queues (v2).** When multisite lands (doc 00 §7), do we partition queues by tenant or share with per-tenant rate limits? Likely the latter, but worth a design pass when multisite is real.
3. **Priority preemption.** If `critical` ever does saturate, do we add cooperative preemption (handler checks a "yield" channel and returns `RetryLater`)? Possible but invasive.
4. **Worker autoscaling.** Run worker replicas as a stateful set tied to queue depth metrics. The hooks exist (depth metric, per-queue worker bind) but the autoscaler policy is undesigned.
5. **Transactional enqueue.** Today, enqueueing inside a DB transaction can desync: TX commits but Redis write fails, or vice versa. For most uses (revalidate, search reindex) this is fine; eventual consistency catches up. For webhook delivery on a critical event, we want at-least-once with TX guarantees. The `pending_critical_tasks` table (§19.1) gives us at-least-once but with a reconciler delay. A move to River (Postgres-backed) solves this; until then, the reconciler is the answer.
6. **Plugin job sandboxing across workers.** A plugin's WASM module is loaded per-worker. Hot reload on plugin upgrade across all workers is currently a "restart worker fleet" operation. A version-pinned dispatch (§4.4) prevents stale execution but the unused old module still occupies memory until restart. Cleanup policy TBD.
7. **DLQ payload privacy retention.** §6 redacts sensitive fields in the admin UI, but the raw payload still lives in Redis archive for 14 days. We may want shorter retention (or encryption-at-rest) for tasks tagged sensitive.
8. **Plugin cron quotas.** Today each plugin gets one rate-limit budget that covers cron + on-demand enqueues. Plugins with heavy cron may struggle to also handle on-demand work. Consider separate cron and on-demand buckets.

---

## 21. Document references

- **00 Architecture Overview** — confirms Asynq as the choice and the Redis dependency.
- **01 Core CMS** — owns `search.reindex.*` semantics, revision retention policy that `revisions.purge` honors.
- **02 Plugin System** — owns the WASM sandbox, the `queue` capability, the manifest cron schema. Depends on this doc for `plugin.activate/deactivate/uninstall.cleanup` task contracts and the per-plugin scoping.
- **04 Block Editor** — owns `autosave.purge` retention.
- **05 Admin & Webhooks** — owns the webhook *subscription* surface and event catalog. Depends on this doc for `webhook.deliver` mechanics (§14).
- **06 Auth & Permissions** — owns `email.password_reset / verify`, `auth.*.cleanup`, audit table that `audit.archive` rotates.
- **07 Media & Performance** — owns image/video pipeline, cache tags, ISR strategy. Depends on this doc for `media.*`, `cache.*`, `revalidate.next` task contracts.
- **08 Migration & Compatibility** — owns the importer. Depends on this doc for `migrate.batch.*`, `migrate.verify` task contracts and the chunked-coordinator pattern.
- **10 Observability** (forward-ref) — owns the metrics dashboard, alerts, log schema. This doc names what we emit; doc 10 names where it goes.
