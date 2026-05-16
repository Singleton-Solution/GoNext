# 10. Observability

> Logs, metrics, traces, events, errors, and RUM for the WordPress-clone. Owns "how do we know what the system is doing in production." Gap review §A2 flagged observability as missing — this doc fills it. Reader profile: senior SRE.

Cross-refs:
- `00-architecture-overview.md` — stack (Go API, Next.js public+admin, Postgres, Redis, Asynq, S3, WASM via `wazero`).
- `02-plugin-system.md` — WASM host, capabilities, fuel/timeout.
- `06-auth-permissions.md` — security audit log (distinct from operational events; see §6).
- `07-media-performance.md` — RUM placeholder (§19); image pipeline metrics.
- `09-review-gaps.md` §A2 — origin of this doc.
- (Forward) `11-deployment-ops.md` — log shipping pipeline, retention storage, on-call rotation.

---

## 1. Principles

Observability is one of three pillars of operability (the others being reproducibility and recoverability). Decisions in this doc are driven by:

1. **Vendor-neutral by default.** We adopt OpenTelemetry (OTel) as the unified instrumentation API. Backends are pluggable; nothing in app code knows the difference between Tempo, Honeycomb, Jaeger, or a self-hosted collector.
2. **Plugin behavior is first-class.** Plugins run untrusted code in a sandbox. Operators *must* be able to attribute latency, errors, and resource pressure to a specific plugin. Every signal carries plugin attribution where applicable.
3. **Cardinality discipline.** High-cardinality labels (user_id, post_id, request_id) belong in logs/traces, *never* in metrics. Plugins cannot mint new label dimensions without host enforcement.
4. **Cheap by default, expensive on demand.** Sampling, ring-buffered debug, head-based traces. Operators can flip a flag to ramp up verbosity for a known incident window.
5. **Self-hostable.** A site owner running this on a single VPS must get useful signals without paying for SaaS. SaaS backends are an optional add-on, not a prerequisite.
6. **Twelve-factor.** Logs to stdout. Metrics scraped over HTTP. Traces exported via OTLP. The app does not own retention or shipping — the deployment does (forward-ref doc 11).
7. **Privacy-conscious.** No third-party RUM trackers ship in the box. IPs anonymized. PII redacted in logs.

---

## 2. Pillars: what we adopt, what we defer

| Pillar | v1 | v2 | Rationale |
|---|---|---|---|
| **Structured logs** | yes (JSON, `slog`) | unchanged | Required for any prod debugging. |
| **Metrics** | yes (Prom-compatible + OTLP) | add long-term storage tier | Required for SLOs/alerts. |
| **Distributed traces** | yes (OTel, head-sampled) | tail-based sampling | Critical because WASM plugin calls span process boundaries (host ↔ guest) and we need attribution. |
| **Continuous profiling** | defer | Pyroscope/Parca | Nice-to-have. Adds shipping cost and a backend. Add when we have evidence of unattributable CPU. |
| **eBPF host-level metrics** | defer | optional add-on for self-host appliance | Useful but high ops cost. |
| **Real-User Monitoring (RUM)** | yes (in-house) | richer dashboards | Doc 07 §19 promised it; required for honest LCP/INP claims. |
| **Synthetic monitoring** | CLI bench + external uptime ping | full SLO synthetic suite | The CLI lands with v1. External SaaS check is a SaaS-tier feature. |
| **Audit log (security)** | doc 06 (separate) | unchanged | Operational events ≠ audit events; see §6. |
| **Error tracking** | yes (Sentry-protocol; GlitchTip recommended self-host) | unchanged | Errors-as-tickets is a different access pattern from logs/traces. |

We **adopt OpenTelemetry as the unified standard** for all three of logs, metrics, and traces, with these caveats:

- **Metrics:** OTel metrics SDK is stable in Go. We export *both* a Prometheus `/metrics` scrape endpoint (via `otelprom` exporter) *and* OTLP push. Why both: most self-hosters run Prometheus; SaaS users want OTLP push to their vendor.
- **Logs:** OTel log SDK in Go is younger. We emit logs via `slog` to stdout (12-factor). The OTel collector tails stdout (or the container log driver does) and forwards. We do *not* embed an OTel log exporter in the app process to avoid coupling app uptime to collector health.
- **Traces:** OTel native. OTLP/gRPC out of the app, head-sampled.

---

## 3. Data flow

```
   ┌──────────────────────┐     ┌──────────────────────┐
   │ Next.js public       │     │ Next.js admin        │
   │ (web vitals beacon,  │     │ (admin UI traces,    │
   │  RUM marks, frontend │     │  RUM marks)          │
   │  console errors)     │     │                      │
   └──────┬───────────────┘     └──────┬───────────────┘
          │ traceparent header        │ traceparent header
          │ POST /_/rum/beacon        │
          ▼                            ▼
   ┌───────────────────────────────────────────────────┐
   │              Go API server                        │
   │  - slog JSON → stdout                             │
   │  - /metrics  (Prometheus scrape)                  │
   │  - OTLP push (traces, optional metrics, events)   │
   │  - error-tracker SDK (Sentry protocol)            │
   │                                                   │
   │  WASM plugin host wraps every hook in a span,     │
   │  emits per-plugin metrics, namespaces plugin logs │
   └────────┬───────────────────┬───────────────┬──────┘
            │ stdout            │ OTLP          │ Sentry
            │                   │               │  protocol
            ▼                   ▼               ▼
   ┌───────────────┐   ┌───────────────────┐  ┌──────────────┐
   │ container log │   │ OTel Collector    │  │ GlitchTip /  │
   │ driver        │──▶│ (sidecar/DaemonSet│  │ Sentry self- │
   │ (stdout tail) │   │  or shared)       │  │ hosted       │
   └──────┬────────┘   └──┬──────┬──────┬──┘  └──────────────┘
          │               │      │      │
          ▼               ▼      ▼      ▼
   ┌─────────┐   ┌──────────┐ ┌──────┐ ┌────────────┐
   │ Loki /  │   │ Prom /   │ │Tempo │ │ Long-term  │
   │ FluentD │   │ Mimir /  │ │ Honey│ │ archive    │
   │ → SaaS  │   │ Cortex   │ │ comb │ │ (S3/Parquet)│
   └─────────┘   └──────────┘ └──────┘ └────────────┘
```

The collector is the seam. App processes export to one address (`OTEL_EXPORTER_OTLP_ENDPOINT`); operators rewire the collector to whatever backend they have. Self-hosters can run the collector in `localhost` mode and forward to Loki + Prom + Tempo. SaaS users point it at Honeycomb/Datadog/etc.

---

## 4. Structured logging

### 4.1 Library choice (Go)

**`log/slog` from the stdlib.** Rejected alternatives in §15.

```go
package logger

import (
    "context"
    "io"
    "log/slog"
    "os"

    "github.com/gonext/gonext/internal/build"
)

// Setup wires the process-wide default logger.
// All app code uses slog.Default() or a context-scoped logger.
func Setup(w io.Writer, level slog.Leveler) *slog.Logger {
    if w == nil {
        w = os.Stdout
    }
    h := slog.NewJSONHandler(w, &slog.HandlerOptions{
        AddSource:   false, // too noisy; we have traces for that
        Level:       level,
        ReplaceAttr: redactAttr, // see §4.4
    })
    base := slog.New(h).With(
        slog.String("service", "gonext-api"),
        slog.String("version", build.Version),
        slog.String("commit", build.Commit),
    )
    slog.SetDefault(base)
    return base
}

// FromContext extracts a request-scoped logger; falls back to default.
func FromContext(ctx context.Context) *slog.Logger {
    if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
        return l
    }
    return slog.Default()
}

// WithRequest enriches the logger for the request scope.
func WithRequest(ctx context.Context, traceID, spanID, requestID, userID string) context.Context {
    l := FromContext(ctx).With(
        slog.String("trace_id", traceID),
        slog.String("span_id", spanID),
        slog.String("request_id", requestID),
        slog.String("user_id", userID),
        slog.String("tenant_id", "default"), // placeholder for v2 multitenancy
    )
    return context.WithValue(ctx, loggerKey{}, l)
}

type loggerKey struct{}
```

### 4.2 Format

JSON, one object per line, no pretty-printing in any environment. Local dev uses a wrapping pretty-printer in the run script if the operator wants colorized output, but the program itself emits JSON unconditionally so that local logs are the same shape as prod logs.

### 4.3 Required fields

Every log line carries:

| Field | Source | Notes |
|---|---|---|
| `time` | slog default | RFC3339Nano, UTC. |
| `level` | slog | `DEBUG`, `INFO`, `WARN`, `ERROR`. We do not use `FATAL` from libraries; we panic-and-recover. |
| `msg` | slog | Human-readable, short, no template expansion (use attrs). |
| `service` | base logger | `gonext-api`, `gonext-worker`, `gonext-next-public`, `gonext-next-admin`. |
| `version` | base logger | Build version. |
| `commit` | base logger | Short SHA. |
| `trace_id` | request middleware | W3C trace ID (32 hex). Empty if outside request. |
| `span_id` | request middleware | Current span (16 hex). |
| `request_id` | request middleware | ULID; survives even when trace is unsampled. |
| `user_id` | auth middleware | Empty for anonymous. Numeric, *not* email. |
| `tenant_id` | placeholder | Always `default` in v1; reserved for v2. |
| `plugin_slug` | plugin-host re-emit | Only present on lines from `host.log`. |

Additional contextual attrs are appended by the call site (`route`, `method`, `status`, `duration_ms`, etc.).

### 4.4 Levels

| Level | When |
|---|---|
| `DEBUG` | Off in prod. Verbose request bodies, SQL parameters, WASM fuel ticks, ISR cache decisions. Behind `GONEXT_DEBUG=1` flag (§13). |
| `INFO` | Lifecycle events: server start/stop, plugin install/uninstall/activate/deactivate, theme switch, migration run, background job start/finish. Successful destructive admin actions. |
| `WARN` | Recoverable degraded states: slow query (>p99 threshold), retry succeeded after attempt N, plugin hook returned within timeout but >50% of budget, fallback path taken (e.g. Redis down → in-memory cache). |
| `ERROR` | Unrecoverable for the current request/job: 5xx response, panic recovered, plugin hook timed out or OOMed, DB connection unavailable, Asynq job dead-lettered. Every `ERROR` should have a corresponding metric increment and an error-tracker capture. |

We deliberately do not have `TRACE`. If you want trace-level detail, use a distributed trace.

### 4.5 Redaction

Implemented in `slog.HandlerOptions.ReplaceAttr`:

| Pattern | Treatment |
|---|---|
| `password`, `password_hash`, `secret`, `api_key`, `token`, `bearer`, `cookie`, `set-cookie`, `authorization` | Replaced with `***`. Key match is case-insensitive. |
| `email` | Partial mask: `j***@example.com`. Local part first char + ellipsis; domain preserved (we need the domain for ops). |
| `phone` | Replaced entirely with `***` if matched. |
| Values matching JWT regex (`^eyJ[a-zA-Z0-9_-]+\.`) | Replaced with `***`. Defense-in-depth in case a token leaks into an unnamed field. |
| Values matching credit-card Luhn-passing 13–19 digit sequences | Replaced with `***`. |
| Values matching `ssn`-shaped 9-digit groups | Replaced with `***`. |

The redactor is **fail-open on parse error** but **fail-closed on match**: if redaction logic itself throws, we drop the value rather than emit raw.

Redaction is a defense, not the primary control. The primary control is: developers do not log secrets. The redactor catches mistakes. Periodic log audit (manual sample) verifies.

### 4.6 Sampling

Most log lines are not sampled. Two exceptions:

- **Access logs (one per request).** Always emitted at `INFO`. Not sampled — this is the access log.
- **High-volume debug lines** behind `GONEXT_DEBUG_SAMPLE=N`: emit 1 of every N. Used in incident debugging where you want to see *some* of a flood, not all of it. Off by default.

We do not sample `WARN` or `ERROR`. Ever.

### 4.7 Destinations

The app writes to **stdout only**. Shipping is the deployment's responsibility:

- Docker / Kubernetes: container runtime captures stdout; Fluent Bit / Vector tails and ships to Loki / S3 / SaaS.
- systemd: journald captures; `journalctl --output=json` → forwarder.
- Bare-metal: a sidecar process tails the file the operator chose.

This is doc 11 territory. The app is unaware.

### 4.8 Logging from plugins

Plugins (WASM) call into the host:

```
host.log(level u32, msg_ptr u32, msg_len u32, fields_ptr u32, fields_len u32) -> u32
```

The host:

1. Validates `level` is one of the four (`DEBUG`/`INFO`/`WARN`/`ERROR`); silently downgrades unknowns to `INFO`.
2. Reads the message (UTF-8) and the fields (a flat MessagePack map, capped at 4 KiB total).
3. Re-emits via `slog` with:
   - `plugin_slug` and `plugin_version` host-added.
   - All user-supplied keys prefixed with `plugin.` to avoid colliding with reserved keys.
   - The redactor runs over the fields just like host-emitted logs.
4. Increments `gonext_plugin_log_lines_total{plugin_slug, level}`.
5. **Caps log volume per plugin per minute.** A token bucket: 600 lines/min (10/s) per plugin, burst 60. Excess lines are dropped silently except for a single `plugin.log.throttled` WARN per minute carrying the dropped count. This prevents a chatty plugin from blowing the log budget.

Plugins do not get raw stdout. Plugins do not get to set `trace_id`, `user_id`, `service`, or any reserved field — the host strips and overwrites those.

### 4.9 Frontend (Next.js) logs

Next.js servers (public + admin) also emit JSON to stdout via a thin wrapper around `pino`. Required fields match the Go app. Frontend log lines that originate from a request that crossed the API boundary carry the *same* `trace_id` — we propagate W3C `traceparent` from the API response into RSC and back. Browser-side `console.error` is captured by the error tracker (§6), not by the log pipeline.

---

## 5. Metrics

### 5.1 Stack

- **Prometheus-compatible scrape** at `/metrics` on every Go process. Uses `prometheus/client_golang` directly and `otelprom` for OTel-instrumented bits. Path is configurable; default exposes on the admin-bound port, not the public port (so an internet scrape can't hit it).
- **OTLP push** also available behind `OTEL_METRICS_EXPORTER=otlp` for environments where pull-based scrape is awkward (serverless, lambdas, network-segmented).
- Histograms use **exponential buckets** sized for the signal:
  - Request duration: `[0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]` seconds.
  - Query duration: same.
  - WASM fuel: `[100, 1k, 10k, 100k, 1M, 10M, 100M, 500M]` fuel units (fuel cap is set per-plugin; see doc 02).
- **Native histograms** are preferred where the backend supports them (Prom 2.40+, OTel native); we ship both representations for portability.

### 5.2 Sample metric registration

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    HTTPRequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "gonext",
            Subsystem: "http",
            Name:      "request_duration_seconds",
            Help:      "HTTP request duration, by route template and method.",
            Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
            // NativeHistogramBucketFactor: 1.1, // enable native histograms when backend supports
        },
        []string{"route", "method", "status_class"}, // status_class = "2xx"/"3xx"/"4xx"/"5xx"
    )

    PluginHookFuelUsed = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "gonext",
            Subsystem: "plugin",
            Name:      "hook_fuel_used",
            Help:      "WASM fuel consumed per plugin hook invocation.",
            Buckets:   []float64{100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000, 100_000_000, 500_000_000},
        },
        []string{"plugin_slug", "hook"},
    )
)
```

Routes use the **template form**, not the materialized URL. `/posts/:id`, not `/posts/42`. The HTTP middleware extracts the matched route pattern from the router.

### 5.3 Metric catalog

All metrics live under the `gonext_` prefix (Prom) / `gonext.*` namespace (OTel). The table is canonical; new metrics require a doc PR.

#### HTTP

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_http_request_duration_seconds` | histogram | `route`, `method`, `status_class` | End-to-end server-side latency. |
| `gonext_http_requests_in_flight` | gauge | `route`, `method` | Concurrent in-flight requests. |
| `gonext_http_requests_total` | counter | `route`, `method`, `status` (full code) | Request count. |
| `gonext_http_request_size_bytes` | histogram | `route`, `method` | Inbound body size. |
| `gonext_http_response_size_bytes` | histogram | `route`, `method`, `status_class` | Outbound body size. |
| `gonext_http_client_disconnect_total` | counter | `route` | Client closed before response done. |

#### Database

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_db_query_duration_seconds` | histogram | `query_name`, `op` (`select`/`insert`/`update`/`delete`) | Per-named-query latency. |
| `gonext_db_pool_open_connections` | gauge | `db` (`primary`/`replica`) | Current open conns. |
| `gonext_db_pool_in_use` | gauge | `db` | Conns currently used by callers. |
| `gonext_db_pool_idle` | gauge | `db` | Idle conns. |
| `gonext_db_pool_wait_seconds_total` | counter | `db` | Cumulative time goroutines waited for a conn. |
| `gonext_db_pool_wait_count_total` | counter | `db` | Number of pool-wait events. |
| `gonext_db_errors_total` | counter | `db`, `kind` (`connect`/`query`/`tx`) | Error count by kind. |
| `gonext_db_replication_lag_seconds` | gauge | `replica` | Seconds behind primary (where replicas exist). |
| `gonext_db_tx_duration_seconds` | histogram | `tx_name` | Transaction duration. |

`query_name` is a code-defined symbol (e.g. `posts.list_published`), **not** the SQL text or a hash of it. Caps cardinality at the ~hundreds.

#### Redis

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_redis_command_duration_seconds` | histogram | `command` (`GET`/`SET`/`HGET`/...) | Per-command latency. |
| `gonext_redis_errors_total` | counter | `command`, `kind` (`network`/`timeout`/`protocol`) | Errors. |
| `gonext_redis_evictions_total` | counter | (none) | From `INFO stats` `evicted_keys`. Scraped periodically. |
| `gonext_redis_connections` | gauge | (none) | Pool open connections. |

#### Cache (multi-layer)

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_cache_hits_total` | counter | `layer` (`http`/`isr`/`fragment`/`object`/`block`) | Cache hits. |
| `gonext_cache_misses_total` | counter | `layer` | Cache misses. |
| `gonext_cache_invalidations_total` | counter | `tag_namespace` (e.g. `post`/`taxonomy`/`media`/`plugin`) | Invalidations by tag root. |
| `gonext_cache_stampede_dedup_total` | counter | `layer` | Thundering-herd suppressions (singleflight wins after a follower). |
| `gonext_cache_set_bytes` | histogram | `layer` | Size of cache values stored. |

`tag_namespace` is the root of the tag string before the colon. Full tag (`post:123`) would be unbounded; namespace (`post`) is fixed.

#### Background jobs (Asynq)

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_asynq_queue_depth` | gauge | `queue` (`default`/`critical`/`low`/`plugin`) | Pending jobs. |
| `gonext_asynq_active_jobs` | gauge | `queue` | Currently executing. |
| `gonext_asynq_job_duration_seconds` | histogram | `task_type` | Run duration by task type. |
| `gonext_asynq_job_retries_total` | counter | `task_type` | Retries. |
| `gonext_asynq_job_failed_total` | counter | `task_type`, `kind` (`error`/`panic`/`timeout`) | Failures. |
| `gonext_asynq_dlq_size` | gauge | (none) | Dead-letter queue size. |
| `gonext_asynq_processing_lag_seconds` | gauge | `queue` | Age of oldest pending job. |

`task_type` is the code-defined name (e.g. `media:generate_variants`, `plugin:tick`).

#### WASM / plugin runtime

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_plugin_instances_live` | gauge | `plugin_slug` | Currently-instantiated module instances (pooled). |
| `gonext_plugin_invocations_total` | counter | `plugin_slug`, `hook` | Total host→guest hook calls. |
| `gonext_plugin_hook_duration_seconds` | histogram | `plugin_slug`, `hook` | Wall-clock per hook. |
| `gonext_plugin_hook_fuel_used` | histogram | `plugin_slug`, `hook` | Fuel consumed (see §5.1 buckets). |
| `gonext_plugin_hook_timeout_total` | counter | `plugin_slug`, `hook` | Hook hit wall-clock timeout. |
| `gonext_plugin_hook_fuel_exhausted_total` | counter | `plugin_slug`, `hook` | Hook hit fuel cap. |
| `gonext_plugin_hook_oom_total` | counter | `plugin_slug`, `hook` | Linear-memory grow request denied or panic. |
| `gonext_plugin_hook_errors_total` | counter | `plugin_slug`, `hook`, `kind` (`trap`/`abi`/`panic`) | Guest-side errors. |
| `gonext_plugin_module_compile_duration_seconds` | histogram | `plugin_slug` | Cost of compiling a wasm module. |
| `gonext_plugin_module_size_bytes` | gauge | `plugin_slug` | Module on disk. |
| `gonext_plugin_log_lines_total` | counter | `plugin_slug`, `level` | From `host.log`. |
| `gonext_plugin_log_dropped_total` | counter | `plugin_slug` | Throttled lines (§4.8). |

#### Plugin host ABI

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_plugin_abi_calls_total` | counter | `plugin_slug`, `capability` (`db`/`http`/`kv`/`media`/`observability`/...), `func` | Per-ABI-function call count. |
| `gonext_plugin_abi_errors_total` | counter | `plugin_slug`, `capability`, `func`, `kind` | ABI call errors (e.g. permission denied, quota). |
| `gonext_plugin_abi_duration_seconds` | histogram | `plugin_slug`, `capability`, `func` | ABI call latency (mostly the host work, not the guest). |
| `gonext_plugin_abi_permission_denied_total` | counter | `plugin_slug`, `capability`, `func` | Plugin tried a capability it doesn't hold. |

#### ISR / revalidate

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_isr_revalidate_calls_total` | counter | `kind` (`tag`/`path`/`bulk`) | Revalidation webhook calls. |
| `gonext_isr_revalidate_failures_total` | counter | `kind`, `reason` (`http_5xx`/`timeout`/`auth`) | Failures. |
| `gonext_isr_revalidate_duration_seconds` | histogram | `kind` | Latency of the revalidate call. |
| `gonext_isr_tag_fanout_size` | histogram | `tag_namespace` | How many paths a tag invalidates. |

#### Media

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_media_upload_bytes_total` | counter | `mime_class` (`image`/`video`/`doc`/`other`) | Bytes ingested. |
| `gonext_media_upload_duration_seconds` | histogram | `mime_class` | Upload pipeline duration. |
| `gonext_media_variant_generate_duration_seconds` | histogram | `variant` (`thumbnail`/`medium`/`large`/`webp`/`avif`) | Variant generation. |
| `gonext_media_variant_failures_total` | counter | `variant`, `kind` | Failures. |
| `gonext_media_thundering_herd_dedup_total` | counter | `variant` | Singleflight wins. |
| `gonext_media_storage_bytes` | gauge | `bucket` | Total bytes in object store (sampled hourly). |

#### Auth

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_auth_login_attempts_total` | counter | `method` (`password`/`oauth`/`magic_link`/`api_token`) | Attempts. |
| `gonext_auth_login_failures_total` | counter | `method`, `reason` (`bad_password`/`no_user`/`locked`/`expired`/`2fa_required`/`2fa_invalid`) | Failures. |
| `gonext_auth_2fa_challenges_total` | counter | `kind` (`totp`/`webauthn`/`backup_code`) | 2FA prompts. |
| `gonext_auth_lockouts_total` | counter | (none) | Accounts locked. |
| `gonext_auth_sessions_active` | gauge | (none) | Active sessions in Redis (sampled). |
| `gonext_auth_token_issued_total` | counter | `kind` (`session`/`api_jwt`/`refresh`) | Tokens issued. |

#### Search

| Metric | Type | Labels | Measures |
|---|---|---|---|
| `gonext_search_query_duration_seconds` | histogram | `engine` (`pg_fts`/`meili`/`typesense`) | Query latency. |
| `gonext_search_zero_results_total` | counter | `engine` | Queries returning zero results. |
| `gonext_search_index_lag_seconds` | gauge | `engine` | For external engines, how stale the index is. |

#### Process / runtime

We expose `process_*` and `go_*` metrics from `client_golang`'s collectors out of the box. No need to re-document those, but operators get GC pause, goroutine count, heap, fds, etc.

### 5.4 Cardinality budget

Cardinality is the cost driver in Prom. We bound it:

| Label | Bound | Source of bound |
|---|---|---|
| `route` | ~150 | Hand-maintained list in code. Router exposes template names. |
| `method` | 7 (`GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/`OPTIONS`) | HTTP. |
| `status` / `status_class` | ~60 / 5 | HTTP. |
| `plugin_slug` | ≤100 (active plugins) | Plugin-host registry; metric retired on uninstall. |
| `hook` | ~60 | Core-defined hook names. Plugins **cannot mint new hook names** in metric labels — the host only emits the metric for *registered* hooks. |
| `capability`, `func` | ≤200 | Host ABI surface is fixed. |
| `task_type` | ~30 | Code-defined. |
| `query_name` | ~300 | Code-defined named queries. |
| `tag_namespace` | ~10 | `post`, `page`, `taxonomy`, `media`, `user`, `plugin`, `theme`, `setting`, `block`, `route`. |
| `variant` | ~10 | Image pipeline-defined. |
| `db`, `replica`, `bucket`, `engine`, `kind` | <10 each | Infra-shaped. |

**Banned labels in metrics:**

- `user_id`, `post_id`, `media_id`, any user-supplied ID — unbounded.
- `request_id`, `trace_id`, `span_id` — every request has one.
- `email`, `ip` — PII and unbounded.
- `url`, `full_path` — use `route` template.
- Anything plugin-attacker-controlled. The plugin host's metric ABI (§11) refuses unknown label keys; values are not free-form strings.

Operators can flip on a `GONEXT_DEBUG_CARDINALITY=1` mode that periodically logs (not metrics) the top-N label values per metric so they can spot a leak. Off by default.

---

## 6. Distributed tracing

### 6.1 Library & transport

OpenTelemetry Go SDK with OTLP/gRPC exporter. Backends tested in CI: **Tempo**, **Jaeger**, **Honeycomb**. Self-hosters typically wire to Tempo via the collector; SaaS users typically point the collector at their vendor's OTLP endpoint.

We do *not* embed multiple exporters in the app. The collector is the fan-out.

### 6.2 Propagation

We use **W3C trace-context** (`traceparent`, `tracestate`) end-to-end. B3 is *not* supported in v1 (one less code path). The propagation chain:

```
Browser   ──traceparent──▶  CDN
CDN       ──traceparent──▶  Next.js (public/admin)
Next.js   ──traceparent──▶  Go API
Go API    ──traceparent──▶  Postgres   (via pg comment trace-context, see below)
Go API    ──traceparent──▶  Redis      (custom span; Redis itself doesn't carry it)
Go API    ──traceparent──▶  Asynq      (in job payload header)
Asynq     ──traceparent──▶  Worker     (continues the trace)
Go API    ──traceparent──▶  WASM host  (span created host-side; guest sees IDs via observability ABI)
```

**Postgres trace propagation** uses `sqlcomment` style: the driver appends `/*traceparent='00-...'*/` to every query. This lets pgBadger / pg_stat_statements correlate slow queries to traces. Disabled by config flag if your DB monitoring strips comments.

**Asynq trace propagation:** when we enqueue a job we set `Headers["traceparent"]` on the task payload. The worker reads it, creates a `consumer` span as the child of the producer's span, and propagates from there. Trace links are used when one trace fans out to many jobs (we attach a `link` to the parent span rather than make the parent the literal parent of N hour-later spans).

### 6.3 Span naming

`<component>.<operation>` with these rules:

| Component | Examples |
|---|---|
| `http.server` | `http.server GET /posts/:id` |
| `http.client` | `http.client POST https://upstream.example/path` |
| `db` | `db.query posts.list_published`, `db.tx posts.create` |
| `redis` | `redis.cmd GET`, `redis.pipeline` |
| `cache` | `cache.lookup http`, `cache.invalidate post` |
| `asynq.producer` | `asynq.enqueue media:generate_variants` |
| `asynq.consumer` | `asynq.consume media:generate_variants` |
| `plugin.hook` | `plugin.hook seo:filter_meta` |
| `plugin.abi` | `plugin.abi observability.span_start` |
| `isr.revalidate` | `isr.revalidate tag` |
| `media.variant` | `media.variant generate webp` |
| `auth` | `auth.login password`, `auth.session.lookup` |

Names are **stable strings, not interpolated**. The dynamic part is in attributes (`http.route`, `db.query_name`, `plugin.slug`).

### 6.4 Required span attributes

Per OTel semconv where applicable, plus our own:

- `http.server` span: `http.request.method`, `http.route`, `http.response.status_code`, `url.path` (without query), `client.address` (truncated to /24 for IPv4, /48 for IPv6), `user.id` (if authed).
- `db.query` span: `db.system="postgresql"`, `db.namespace`, `gonext.db.query_name`, `db.operation.name`, `db.rows_returned` (capped at log scale buckets so it's an attribute, not a free integer for indexing).
- `plugin.hook` span: `gonext.plugin.slug`, `gonext.plugin.version`, `gonext.hook.name`, `gonext.wasm.fuel_used`, `gonext.wasm.memory_pages`, `gonext.wasm.timeout_hit` (bool), `gonext.wasm.instance_age_ms`.

### 6.5 WASM plugin spans

This is the most important part of tracing for this system.

```
http.server GET /posts/:id
├── cache.lookup http  (miss)
├── db.query posts.get_by_slug
├── plugin.hook the_content
│   ├── plugin.hook seo:filter_meta            attrs: plugin.slug=seo, fuel_used=1.2M
│   │   └── plugin.abi db.query                attrs: capability=db, func=query
│   │       └── db.query plugin_seo.lookup_meta
│   ├── plugin.hook contact-form:filter_content attrs: plugin.slug=contact-form, fuel_used=320k
│   └── plugin.hook analytics:filter_content    attrs: plugin.slug=analytics, fuel_used=80k
├── theme.render single-post
└── http.response
```

The host creates the outer `plugin.hook <hookname>` span around the dispatch loop, and one inner span per registered handler. Inner span carries `plugin.slug`, `plugin.version`, fuel/memory state at exit, and whether the result was a timeout/error.

Plugins can emit **their own sub-spans** via host ABI:

```
host.span_start(name_ptr, name_len, attrs_ptr, attrs_len) -> u64  // span handle
host.span_set_attr(handle, k_ptr, k_len, v_ptr, v_len) -> u32
host.span_add_event(handle, name_ptr, name_len, attrs_ptr, attrs_len) -> u32
host.span_end(handle, status u32) -> u32
```

Constraints:
- Span names are prefixed `plugin.<slug>.<name>` by the host, regardless of what the plugin passed. Prevents plugin-named spans masquerading as core spans.
- Attribute keys from plugins are prefixed `plugin.<slug>.` similarly.
- Max 32 attributes per plugin span, max 64 events per span, max depth 8. Excess is dropped with a counter increment (`gonext_plugin_span_dropped_total{plugin_slug, reason}`).
- Plugin spans are *children of the current host hook span*, never free-standing. A plugin cannot start a span outside a hook context.

### 6.6 Sampling

**Head-based, 10% default.** Sampling decision made at the edge (CDN/Next.js or Go API root span when no upstream trace exists). Persisted in `tracestate`.

Always-sample overrides:
- `?_trace=on` query parameter on internal admin tools (capability-gated middleware turns this into an always-sample for the request).
- Requests with `X-Debug-Trace: 1` from a trusted IP allowlist.
- 100% sample for the first 10 requests after process start (warm-up visibility).
- 100% sample for any request that ends in 5xx (head-based override: we sample first, decide to keep on response).

**Tail-based sampling** is run at the **collector**, not the app. The collector keeps:
- All traces with any span status = ERROR.
- All traces longer than p95 of their root-span operation (rolling window).
- 10% of normal traces (matching head-based; the head decision is honored).
- All traces with at least one `plugin.hook` span that hit a fuel/timeout cap.

Tail-based requires a stateful collector pool. Self-host single-node deployments can fall back to "head-only + force-keep errors via per-span sampler" without tail.

### 6.7 Trace ID surfacing

The Go HTTP middleware adds `x-trace-id: <32 hex>` to **every** response, even sampled-out responses. Users hitting a bug can paste this into a support ticket and we can either look the trace up (if sampled) or attach the request_id to a future force-sample if not. Error pages in the public site and admin show the trace ID and request ID under a "details" disclosure.

We do **not** put trace IDs in URLs (privacy / referer leakage).

---

## 7. Events vs the audit log

Two distinct log streams:

| Stream | Owner | Examples | Storage | Retention |
|---|---|---|---|---|
| **Security audit log** | doc 06 | Login success/fail, password change, role assignment, capability grant, API token mint/revoke, 2FA enrollment, user creation. | Append-only Postgres table `audit_log`, structured columns, signed batches optional. | Configurable; default 1 year. |
| **Operational events** | this doc | Deploy started/finished, plugin installed/activated/deactivated/uninstalled, theme switched, cache flushed, migration run, setting updated, scheduled job triggered. | In-process event bus → (a) audit log writer for events that double as security-relevant, (b) OTel event/log exporter, (c) admin "activity feed" view. | 90 days operational; security-relevant ones go to audit (1 year). |

The rule: if a regulator or a forensic responder cares, it's audit. If only the operator cares, it's operational. Some events fan out to both (e.g. "plugin X installed" is operational *and* security-relevant — the audit writer subscribes).

Operational events are emitted as **OTel log records with an `event.name` attribute** rather than as a separate signal, per OTel events conventions. They are tagged severity `INFO` typically; deploy/rollback get `NOTICE`.

The event bus is in-process, synchronous, bounded queue. Subscribers must be non-blocking; the audit writer batches.

---

## 8. Error tracking

### 8.1 Backend recommendation

**GlitchTip** (self-host, Sentry-protocol-compatible, AGPL) is the default recommendation for self-hosters. **Sentry self-hosted** is recommended for larger ops who want the full Sentry UI. Both speak the Sentry SDK protocol so app code is identical.

### 8.2 SDK

Go: `github.com/getsentry/sentry-go`. Next.js: `@sentry/nextjs`.

Wired to:
- Capture every panic-recovered in middleware (with stack), tagged with the trace ID and request ID.
- Capture every `slog.Error` line above a threshold via a custom `slog.Handler` that mirrors to Sentry. We don't mirror everything (cost); we mirror lines with `err` attribute set.
- Capture front-end uncaught errors and rejected promises.

### 8.3 Grouping

We override Sentry's default grouping for two scenarios:

- **Plugin errors:** fingerprint = `plugin:<slug>:<wasm-trap-kind>` so a misbehaving plugin appears as one issue per failure mode, not per call site.
- **DB errors:** fingerprint includes `query_name` not the SQL text (which often contains parameter hints).

### 8.4 Source maps

CI builds Next.js with `hidden-source-map`, uploads source maps to the Sentry/GlitchTip release artifacts endpoint, then deletes them from the public bundle. This is wired into the deploy doc (11) but the app config lives in `next.config.mjs`.

### 8.5 WASM error surfacing

When a plugin hook traps or returns an error, the host:

1. Reads the guest-side error string (if the guest wrote one to a known memory region via the error ABI).
2. Captures with tags `plugin.slug`, `plugin.version`, `hook.name`, `wasm.trap.kind`, `wasm.fuel_used`, `wasm.fuel_remaining`, `wasm.memory_pages`.
3. Includes the host stack and, if available, the wasm-source-map-translated guest stack. (Plugin authors upload sourcemaps via the plugin manifest; doc 02 §SDK.)
4. Increments the corresponding metric and emits an `ERROR` log line.

The capture is rate-limited per (plugin, trap-kind) at 1/s so a tight error loop in one plugin doesn't drown the tracker.

---

## 9. Real-User Monitoring (RUM)

Doc 07 §19 reserves "in-house RUM." Here's what ships.

### 9.1 Signals captured

Per page view:
- **Web Vitals**: LCP, INP, CLS, FCP, TTFB. Captured via the `web-vitals` npm package, called on `visibilitychange` / `pagehide`.
- **Navigation type**: navigate, reload, back-forward, prerender.
- **Connection info**: `effectiveType` (4g/3g/2g/slow-2g), `saveData` boolean. *Not* downlink/RTT (fingerprinting risk).
- **Document timing**: dom-content-loaded, load-event-end.
- **Custom marks**: themes and plugins call `gonext.rum.mark(name)` and `gonext.rum.measure(name, startMark, endMark)`. Marks are namespaced by plugin/theme slug.
- **Errors**: count of `window.error` and `unhandledrejection` events (count only; full error goes to error tracker).

### 9.2 Beacon protocol

Endpoint: `POST /_/rum/beacon` on the **Go API** (not Next.js; we want to bypass SSR for this hot path).

Payload (one beacon per page session, sent on unload via `navigator.sendBeacon`):

```json
{
  "v": 1,
  "ts": 1715600000,
  "page": {
    "route": "/posts/:slug",
    "type": "navigate",
    "referrer_origin": "https://google.com"
  },
  "vitals": { "lcp": 1240, "inp": 80, "cls": 0.02, "fcp": 410, "ttfb": 180 },
  "marks": [ { "n": "theme.hero.painted", "t": 320 } ],
  "errors": 0,
  "conn": "4g",
  "device": "desktop"
}
```

Constraints:
- **No user_id, no session cookie value, no full URL.** Route template only.
- **`referrer_origin`** is the origin only, not the path. Same-origin shows as `internal`.
- **IP anonymized server-side** to /24 (IPv4) / /48 (IPv6) before storage.
- **No fingerprinting fields** (user-agent string is hashed to a short canonical "browser+major-version" bucket; raw UA discarded).

### 9.3 Sampling

Default 25% on the public site, 100% on the admin. Configurable. The beacon payload is small (~400 bytes) so 25% on a busy site is the right starting point.

### 9.4 Storage

**ClickHouse recommended** when available; **Postgres fallback**. Rationale: time-series-ish, high write rate, columnar aggregations on `route` and `vitals` are exactly Clickhouse's sweet spot.

For Postgres-only deployments: a partitioned `rum_beacons` table (daily partitions, drop after 30 days) is acceptable up to ~1M beacons/day. Above that, ClickHouse.

### 9.5 Admin surface

Admin → Performance dashboard. Capability `manage_system` (see doc 06).

Cards:
- LCP/INP/CLS p75 trend (7d, 30d).
- Worst routes by LCP p75.
- Worst routes by error rate.
- Device-class breakdown (desktop/tablet/mobile).
- Connection-class breakdown.
- Theme/plugin custom-mark distributions.

### 9.6 Privacy

- No third-party RUM tracker ships by default. Operators can install one as a plugin if they want.
- The beacon is first-party and cannot be repurposed for ad targeting (no user_id, no cookies set on `/_/rum/beacon`).
- A site-owner-visible privacy toggle "Enable in-product performance measurement (anonymous)" defaults to on; disabling stops the beacon JS from loading.

---

## 10. Synthetic monitoring

### 10.1 Local: `gonext bench`

Doc 07 §20 defines the CLI bench. From the observability side, the bench emits **the same OTel signals** as a real client: it sets `traceparent`, hits the API, and the trace shows up alongside production. This means you can A/B compare a bench result against real traffic in the same backend without an extra tool.

The CLI also writes a JSON report to stdout for CI integration.

### 10.2 SaaS: external uptime + latency check

For the hosted SaaS, an external prober from N regions (configurable; default 3: us-east, eu-west, ap-southeast) pings:

- `GET /healthz` — must be 200, expects <100ms p99 from region.
- `GET /` (home page) — must be 200, expects <800ms p95 LCP-equivalent (full HTML).
- `POST /api/v1/auth/login` with a known synthetic account — checks auth path is alive.

Each check emits a metric: `gonext_synth_check_duration_seconds{region, check}`, `gonext_synth_check_failures_total{region, check, reason}`.

SLOs are defined against these (§12).

For self-host: a single-region `gonext-monitor` sidecar is shipped that does the same. Operators can run it from anywhere.

---

## 11. Plugin observability surface

The `observability` capability is granted by default to all plugins (it doesn't access user data; it produces telemetry). Plugins can opt out by manifest. The capability gates four ABI groups: `log`, `metric`, `event`, `span`.

### 11.1 ABI summary

```
// Logging (also see §4.8)
host.log(level, msg, fields)

// Metrics
host.metric_counter_inc(name_ptr, name_len, labels_ptr, labels_len, value f64) -> u32
host.metric_gauge_set (name_ptr, name_len, labels_ptr, labels_len, value f64) -> u32
host.metric_histogram_observe(name_ptr, name_len, labels_ptr, labels_len, value f64) -> u32

// Events (operational)
host.event_emit(name_ptr, name_len, attrs_ptr, attrs_len) -> u32

// Spans (see §6.5)
host.span_start(...)
host.span_set_attr(...)
host.span_add_event(...)
host.span_end(...)
```

### 11.2 Namespacing & enforcement

For metrics:
- Final metric name is `gonext_plugin_<slug>_<name>`. The plugin cannot bypass the prefix.
- Allowed metric name regex: `^[a-z][a-z0-9_]{0,40}$`. Reject otherwise.
- **Label keys** are validated against a per-plugin allowlist declared in the plugin manifest. A plugin declares `metrics.labels: ["operation", "result"]` and any other label key passed at runtime is dropped with a `gonext_plugin_metric_label_rejected_total` increment. This is the host-enforced cardinality dam.
- **Label value cardinality** is bounded per (metric, label): the host tracks the set of distinct values seen and stops accepting new ones once 50 distinct values are observed per process. New values are coerced to `_overflow`. This prevents accidental high-cardinality (e.g. a plugin using `post_id` as a label).
- Plugins cannot register histogram bucket layouts; the host uses a default bucket set. Sufficient for the use cases we expect.

For events:
- `event.name` becomes `plugin.<slug>.<name>`.
- Event payload size capped at 8 KiB.
- Plugins are rate-limited: 60 events/min, burst 30, per plugin. Over-rate events dropped with a counter.

For spans: see §6.5 constraints.

### 11.3 Plugin's own ID-space

Plugins **do not** see global metric/log namespaces. They cannot read other plugins' metrics. They cannot read host-internal metrics. They cannot read trace data for spans they did not create. Observability is write-only from the plugin's perspective.

---

## 12. SLOs and alerting

### 12.1 SLOs

| SLO | Target | Window | Source metric |
|---|---|---|---|
| API availability (5xx rate) | 99.9% | 30d rolling | `gonext_http_requests_total{status_class!="5xx"}` / total |
| API latency (p95) | <250ms for `route ∈ public-read` | 30d | `gonext_http_request_duration_seconds` p95 |
| API latency (p95) admin | <600ms | 30d | same, filtered to admin routes |
| Asynq processing lag | <60s p95 | 30d | `gonext_asynq_processing_lag_seconds` |
| DB replication lag | <5s p99 | 7d | `gonext_db_replication_lag_seconds` |
| ISR revalidate success | >99.5% | 7d | `1 - gonext_isr_revalidate_failures_total/gonext_isr_revalidate_calls_total` |
| Web Vitals — LCP good (≤2.5s) | ≥75% of sessions | 30d | RUM `vitals.lcp` |
| Web Vitals — INP good (≤200ms) | ≥75% of sessions | 30d | RUM `vitals.inp` |
| Synthetic uptime | >99.95% | 30d | `gonext_synth_check_failures_total` |

### 12.2 Alertmanager rules (examples)

```yaml
groups:
- name: gonext-availability
  rules:
  - alert: APIErrorRateHigh
    expr: |
      sum(rate(gonext_http_requests_total{status_class="5xx"}[5m]))
        /
      sum(rate(gonext_http_requests_total[5m])) > 0.005
    for: 10m
    labels: { severity: page }
    annotations:
      summary: "API 5xx rate > 0.5% for 10m"

  - alert: APILatencyP95High
    expr: |
      histogram_quantile(0.95,
        sum by (le, route) (rate(gonext_http_request_duration_seconds_bucket{route=~"/api/.*"}[5m]))
      ) > 0.5
    for: 15m
    labels: { severity: page }

- name: gonext-jobs
  rules:
  - alert: AsynqProcessingLagHigh
    expr: max(gonext_asynq_processing_lag_seconds) > 300
    for: 10m
    labels: { severity: page }

  - alert: AsynqDLQGrowing
    expr: increase(gonext_asynq_dlq_size[1h]) > 50
    for: 0m
    labels: { severity: ticket }

- name: gonext-db
  rules:
  - alert: DBReplicationLagHigh
    expr: max(gonext_db_replication_lag_seconds) > 30
    for: 5m
    labels: { severity: page }

  - alert: DBPoolSaturation
    expr: |
      gonext_db_pool_in_use / on(db) gonext_db_pool_open_connections > 0.9
    for: 10m
    labels: { severity: ticket }

- name: gonext-plugin
  rules:
  - alert: PluginFuelCapHitRate
    expr: |
      sum by (plugin_slug) (rate(gonext_plugin_hook_fuel_exhausted_total[10m])) > 0.1
    for: 15m
    labels: { severity: ticket }
    annotations:
      summary: "Plugin {{$labels.plugin_slug}} hitting fuel cap >0.1/s"

  - alert: PluginTimeoutSpike
    expr: |
      sum by (plugin_slug) (rate(gonext_plugin_hook_timeout_total[5m])) > 0.05
    for: 5m
    labels: { severity: ticket }

- name: gonext-isr
  rules:
  - alert: ISRRevalidateFailures
    expr: |
      sum(rate(gonext_isr_revalidate_failures_total[10m]))
        /
      sum(rate(gonext_isr_revalidate_calls_total[10m])) > 0.05
    for: 15m
    labels: { severity: ticket }
```

### 12.3 Burn-rate alerts

For each SLO we run two burn-rate alerts (the standard Google SRE pair): fast (5m, 14.4× burn) and slow (1h, 6× burn). Both must fire to page. The slow alert alone goes to ticket. This avoids paging on a 30-second blip and avoids missing a slow leak.

Example for the API availability SLO (99.9% over 30d):

```yaml
- alert: APIAvailabilityBudgetBurnFast
  expr: |
    (1 - sum(rate(gonext_http_requests_total{status_class!="5xx"}[5m]))
       / sum(rate(gonext_http_requests_total[5m]))) > (1 - 0.999) * 14.4
  for: 2m
  labels: { severity: page-candidate }

- alert: APIAvailabilityBudgetBurnSlow
  expr: |
    (1 - sum(rate(gonext_http_requests_total{status_class!="5xx"}[1h]))
       / sum(rate(gonext_http_requests_total[1h]))) > (1 - 0.999) * 6
  for: 15m
  labels: { severity: ticket }

# Composite "page" alert: both burn-rate alerts firing
- alert: APIAvailabilityBudgetBurning
  expr: ALERTS{alertname="APIAvailabilityBudgetBurnFast", alertstate="firing"}
    and ALERTS{alertname="APIAvailabilityBudgetBurnSlow", alertstate="firing"}
  labels: { severity: page }
```

### 12.4 Routing

- `severity=page` → on-call rotation.
- `severity=ticket` → JIRA/Linear queue, reviewed next business day.
- `severity=info` → Slack channel only, no notification.

---

## 13. Operational dashboards in the admin UI

The admin's **System Status** page (capability `manage_system`) is not Grafana. It's "is anything obviously broken in the last hour?"

Sections:

1. **Traffic** — requests/sec, error rate, p95 latency. Sparkline last 1h. Sourced from `/metrics` aggregated server-side.
2. **Backing services** — Postgres, Redis, S3, search engine: each shows up/down (last successful health check) and a latency p95 number.
3. **Background jobs** — Asynq queue depths and processing lag. Clicking a queue links to the per-queue detail.
4. **Plugins** — per-plugin row: invocations/min, p95 hook duration, error count last hour, fuel-cap hits. Sorted by error count descending. Operator can click → plugin detail page with more.
5. **Cache** — hit rate per layer.
6. **Recent operational events** — feed of deploy/install/etc. (from §7).

The data source is configurable:
- Default: Prom HTTP API (operator points it at their Prom).
- Fallback when no Prom: an in-process **lightweight time-series sidecar** that retains 24h of pre-aggregated 1m buckets in Redis. Sufficient for a single-VPS deploy that doesn't run Prom.

This page is read-only. Actions (flush cache, restart job, disable plugin) live in their respective subsystem admin pages and require their own capabilities.

---

## 14. Debug mode

`GONEXT_DEBUG=1` at process start enables a coordinated verbose mode:

| Subsystem | Effect of `GONEXT_DEBUG=1` |
|---|---|
| Logging | Level → `DEBUG`. Request/response bodies logged (sized-limited, redacted). |
| DB | `pgx` query log enabled, parameter values **not** logged (still PII risk). |
| Plugin host | WASM fuel tick logging, ABI call argument logging. |
| HTTP | Adds `Server-Timing` header with per-segment durations to responses. |
| Tracing | Forces sampling to 100% for the lifetime of the process. |
| Asynq | Logs every job state transition. |

This flag is **read once at startup**. It does not flip at runtime — runtime would risk inconsistent state across goroutines. To toggle, restart the process.

For per-request debugging without a restart, a `super_admin` can set `X-GoNext-Debug-Request: 1` on a request from their authenticated session. This enables debug logging *only for that request's request_id and trace_id*. The middleware that grants this requires the `super_admin` capability and emits an audit event (§7).

Production deployments must set `GONEXT_DEBUG=0` (default). The admin **System Status** page surfaces a red banner if `GONEXT_DEBUG=1` is detected in production.

---

## 14a. Bonus: dashboards

We ship Grafana dashboards as JSON files in `deploy/dashboards/`. Each is self-contained; importing requires only a Prom datasource named `gonext` (or whatever; templated).

1. **HTTP Overview**: req/s by route, p50/p95/p99 latency by route, error rate by status_class, in-flight requests, top routes by error count, top routes by p95 latency, response size distribution.
2. **Database**: query duration p50/p95 by `query_name` (top 20), pool open/in-use/idle, pool wait p95, error rate by kind, replication lag, slow query log link (deep link to log backend filtered by `query_name`).
3. **Cache**: hit rate per layer over time, invalidations/sec by tag namespace, stampede dedup count, top 20 invalidation tags by count.
4. **Plugins** (per-plugin row, generated): invocations/sec, p95 hook duration, fuel histogram, timeout/OOM rate, log lines/sec, ABI calls/sec broken down by capability.
5. **Asynq queues**: queue depth, processing lag, jobs done/min, failures/min by kind, retry distribution, DLQ size, top task types by failure rate.
6. **RUM**: LCP/INP/CLS p75 over time, distribution by device class, distribution by connection class, worst routes by LCP, custom marks distribution per theme/plugin.
7. **WASM Runtime**: instances live, fuel histogram (all plugins), top plugins by fuel use, OOM rate, compile duration distribution.
8. **Media pipeline**: variants/min, variant generation p95 by variant, failure rate, dedup count, storage growth.

Dashboards are versioned alongside the app. A breaking metric rename requires a dashboard update in the same PR.

---

## 15. Trade-offs & rejected alternatives

### 15.1 Logging library: `slog` vs `zap` vs `zerolog`

| | `slog` | `zap` | `zerolog` |
|---|---|---|---|
| Std lib | yes | no | no |
| Allocations | low | very low | very low |
| Handler ecosystem | growing | mature | mature |
| Ergonomics | clean attr API | builder, verbose | builder, fluent |

**Choice: `slog`.** Stdlib. Good enough perf for our scale (low-millions req/day per node max). Avoids a dependency. Custom `Handler` for redaction is straightforward. `zap` was a real contender on perf but we'd be choosing zap-perf over stdlib-stability for a perf budget we're not hitting. Reconsider if access-log throughput becomes a hot spot.

### 15.2 Prom direct vs OTel-only

Going OTel-only for metrics would be a cleaner story. Rejected because:
- Most self-hosters run Prometheus. They expect `/metrics`. Forcing OTLP push means they also have to run a collector — that's an extra hop and an extra failure domain for the lowest-end deployment.
- OTel metrics SDK in Go is stable but the Prom-equivalence (rate(), histogram_quantile() semantics) has subtle gotchas in OTel histograms vs Prom histograms that we don't want plugin authors to hit.

So we ship both: `/metrics` is the default; OTLP push is an opt-in.

Will revisit when (a) all major Prom backends accept OTLP directly without the collector and (b) the Go OTel metrics SDK matures another year. Note for 2026: this is increasingly fine.

### 15.3 In-house RUM vs Datadog/Akamai mPulse/SpeedCurve

| Path | Pros | Cons |
|---|---|---|
| In-house (chosen) | Privacy. No vendor cost. First-party JS = no third-party cookie. Data we own. Integrates with our own metric stack. | We build & maintain a beacon endpoint and a small UI. |
| 3rd-party SaaS | Mature dashboards. No engineering. | Cost. Adds a third-party script to every page (perf cost, privacy issue). Lock-in. Site owner has to set up the account. |

We chose in-house because the alternative makes us either ship a third-party tracker (privacy/perf hit) or ship nothing (doc 07 promised RUM). A site owner can still install a Datadog/Plausible/etc. plugin if they want — that's their call.

### 15.4 Audit log: separate table vs unified log stream

Rejected: unified log stream tagged "audit." Reasons:
- Auditors expect a queryable, structured store with retention guarantees. A log stream's retention is the log shipper's promise.
- Audit lookups are point queries by user/object/action — table indexes win over log search.
- Audit needs append-only / tamper-evident semantics. Tables can do this (constraint trigger preventing UPDATE/DELETE, optional signed batches). Log streams generally cannot without extra infrastructure.

See doc 06.

### 15.5 Tail-based sampling: collector vs SaaS

We support tail-based sampling at the collector. We do not implement tail-based in the app — that would require buffering spans in-process pending the trace's final status, which blows memory.

Self-hosters who don't want to run a stateful collector pool can stick with head-based + always-sample-errors. SaaS users typically have a tail-based-aware backend.

### 15.6 OpenTelemetry logs SDK

The OTel logs SDK in Go has been stable since 2025 but the operational story (in-app log exporter coupling app uptime to collector availability) is worse than stdout + container-tail. We emit OTel-shaped attributes (`trace_id`, `span_id`, `severity_text`, `event.name`) but stay on stdout transport.

### 15.7 GlitchTip vs Sentry self-hosted

GlitchTip is AGPL; Sentry self-hosted is BSL with a free tier. GlitchTip covers ~80% of Sentry's features and is dramatically lighter. We recommend GlitchTip for self-host because:
- Lower ops cost (single binary + Postgres).
- AGPL is friendly to ecosystem.
- Sentry-protocol-compatible, so swapping later is config-only.

Operators on the larger end who want release health, performance monitoring inside Sentry, etc. should pick Sentry self-hosted. App code doesn't change.

### 15.8 ClickHouse vs Postgres for RUM

RUM is the only signal where Postgres is plausibly the wrong store at scale (high-cardinality groups, time-series aggregations, ~M/day rows). ClickHouse is the right primitive. We don't make it the *only* store because requiring ClickHouse blocks the single-VPS deploy story. The fallback (partitioned Postgres) is fine to ~1M beacons/day.

---

## 16. Cost & retention

| Signal | Default retention | Notes |
|---|---|---|
| Stdout logs | 30 days hot, 90 days archive | Shipped by container runtime; archived to S3 by the deploy pipeline. |
| Audit log | 365 days | Configurable. |
| Metrics | 90 days at full resolution; 1 year downsampled at 5m | Prom is usually the limiter. Long-term tier (Thanos/Mimir) optional. |
| Traces | 7 days | Most traces are noise after a week. Errors retained 30 days via tail-rule. |
| Errors | 90 days | Sentry default. |
| RUM beacons | 30 days hot in Clickhouse; pre-aggregated rollups retained 365 days | Rollups are p50/p75/p95 by route by hour. |
| Operational events | 90 days | Admin "activity feed" reads from this. |

### Cardinality / cost levers

- All plugin metrics live under one allowlist. A plugin going wild can be revoked from the `observability` capability at runtime (admin UI), which stops the bleeding immediately.
- Histograms with native bucket support are 5–10× cheaper than fixed buckets at the same accuracy; we enable when the backend supports.
- RUM sampling default 25% on public, 100% on admin (admin is low volume).
- Trace head sampling 10%; tail keeps errors + slow + 10% of normal.

### Knobs (env)

| Env | Default | Effect |
|---|---|---|
| `GONEXT_LOG_LEVEL` | `INFO` | Floor for log emission. |
| `GONEXT_DEBUG` | `0` | See §13. |
| `GONEXT_DEBUG_SAMPLE` | `0` | Sample-1-of-N for debug-flooded paths. |
| `GONEXT_DEBUG_CARDINALITY` | `0` | Periodic top-label-values logging. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | If set, app pushes OTLP for traces + metrics. |
| `OTEL_TRACES_SAMPLER_ARG` | `0.1` | Head sampler rate. |
| `GONEXT_RUM_SAMPLE_PUBLIC` | `0.25` | RUM beacon sample rate, public site. |
| `GONEXT_RUM_SAMPLE_ADMIN` | `1.0` | Same, admin. |
| `GONEXT_METRICS_ADDR` | `:9090` | Bind for `/metrics`. Separate from public listener. |
| `GONEXT_PLUGIN_LOG_RATE` | `600/min` | Per-plugin log line cap. |
| `GONEXT_PLUGIN_EVENT_RATE` | `60/min` | Per-plugin event cap. |
| `SENTRY_DSN` / `GLITCHTIP_DSN` | unset | Enables error capture if set. |

---

## 17. Implementation phasing

Maps to doc 00 §6 phases:

| Phase | Observability deliverables |
|---|---|
| **P0 — Skeleton** | `slog` JSON logger with redaction. `/metrics` endpoint with `process_*`, `go_*`, HTTP histograms. `x-trace-id` header. |
| **P1 — CMS core** | DB, Redis, cache metrics. Asynq metrics. Sentry/GlitchTip integration. Audit log table (doc 06). |
| **P2 — Editor** | Block render metrics. RUM beacon endpoint + JS snippet. Postgres-backed RUM store. |
| **P3 — Themes** | Theme-mark RUM support. Theme-switch operational event. |
| **P4 — Plugins** | All WASM/plugin metrics. Plugin span ABI. Plugin metric ABI with namespace enforcement. Per-plugin Grafana dashboard generator. |
| **P5 — Migration** | Importer metrics. Specific dashboards for migration runs. |
| **P6 — Polish** | Tail-based sampling at collector. ClickHouse RUM. Burn-rate SLO alerts. Admin System Status page. |

---

## 18. Security considerations specific to observability

- **Log injection.** Plugin-supplied log messages and fields cannot contain control characters that break the JSON line. The `slog` JSON handler escapes; the host additionally rejects `\n` and `\r` in plugin-supplied string values.
- **Trace context spoofing.** A malicious client can forge `traceparent`. This is fine for grouping but we never trust the client's trace ID for authorization or for billing. The Go middleware always *adopts* the incoming trace ID for join-ability but logs both `client_traceparent` and the *server-generated* one when they disagree on parent span ID (rare edge case for cross-tenant pollution).
- **`/metrics` exposure.** The metrics endpoint binds to a separate address (`GONEXT_METRICS_ADDR`) and is not routed through the public listener. If it must share an address with public traffic, basic auth is enforced and the IP allowlist is checked.
- **Admin System Status data leakage.** The dashboard aggregates by route/plugin; it never shows individual user data. The page is capability-gated (`manage_system`).
- **Error-tracker payloads.** Stack traces can contain local variables. The Sentry SDK is configured `attach_stacktrace: true, send_default_pii: false`. Custom `before_send` hook re-runs the redactor on the exception's extras.
- **RUM beacon DoS.** The beacon endpoint is rate-limited per IP (and per anonymized /24) and the payload is size-capped to 4 KiB. Beacons are queued to a bounded in-memory channel; overflow is dropped with a counter.

---

## 19. Test plan

- **Unit:** redactor (must mask password/email/JWT/CC patterns).
- **Unit:** logger context propagation (trace_id flows from middleware through to call sites).
- **Unit:** metric label allowlist enforcement (plugin sending unknown label is rejected).
- **Integration:** end-to-end trace from a synthetic HTTP request through DB and a WASM plugin shows up in the OTLP exporter with all expected spans.
- **Integration:** plugin log rate-limit kicks in at 600/min, drops a known number of subsequent lines, emits the drop counter.
- **Integration:** error-tracker captures a recovered panic with `trace_id` tag present.
- **Load:** `/metrics` scrape cost at 10k req/s baseline must stay <50ms p95 and <5% CPU.
- **Load:** RUM beacon endpoint sustains 5k/s with <10ms p95.
- **Chaos:** turn the OTel collector off; app must keep serving (logs to stdout, metrics still scraped, traces buffered & then dropped on overflow without OOM).
- **Privacy:** automated check on the RUM beacon path that no field can contain a user ID or full URL.

---

## 20. Open questions

1. **Long-term metric store.** Thanos vs Mimir vs Grafana Cloud vs nothing-for-self-host. Forward to doc 11.
2. **Continuous profiling enable-by-default in P6?** Pyroscope is ~free in ops cost now; we might just ship it. Decision pending an actual P5 perf review.
3. **eBPF host-level visibility.** Useful for the SaaS appliance form factor; ops-heavy for self-host. Probably a SaaS-only add-on. Decide before SaaS launch.
4. **OpenTelemetry collector deployment shape.** Sidecar per pod vs DaemonSet vs shared cluster. Forward to doc 11.
5. **Audit log storage backend.** Doc 06 owns this; observability subscribes. If we want signed batches, what's the key-management story?
6. **RUM device classification.** Currently desktop/tablet/mobile from UA. Do we add `app-webview` / `headless`? Cardinality cheap; signal questionable.
7. **Plugin sourcemap upload protocol.** Plugin doc 02 §SDK references it; we depend on it for §8.5. Coordinate so the manifest field exists in P4.
8. **Trace propagation through CDN.** Most CDNs strip `traceparent` by default. We need a per-CDN config (Cloudflare workers, Fastly VCL, Akamai) to preserve it or to mint a fresh root span at the CDN edge. Forward to doc 11.
9. **Cost cap.** Do we want a hard cost cap mode (`GONEXT_OBSERVABILITY_BUDGET=cheap`) that pre-configures sampling/retention to known-safe values for very-small deploys? Likely yes.
10. **Per-tenant observability** for v2 multi-tenant. Tenant_id is a label everywhere already (placeholder); the question is per-tenant *dashboards* and per-tenant cardinality budgets. Defer to v2.
