# Multi-region deployment (v2)

> Status: **design only**. This doc captures the config flags and
> fan-out URLs the v2 multi-region surface will read from. The v1
> binary ships a single-region wiring; the flags here are reserved so
> the v2 cut can land without breaking existing operators' configs.
>
> Tracked in issue #138. Implementation issues fan out from this
> document.

## Scope

A v2 multi-region GoNext deployment runs:

- one **primary region** owning the writable Postgres + Redis
  primary,
- one or more **read replica regions** with Postgres physical replicas
  + Redis cluster nodes,
- a global CDN fronting the public theme,
- a global anycast LB fronting the admin and API surfaces.

This is not a multi-master setup. Writes always go to the primary
region; the read replicas serve reads with bounded staleness.

## Why this matters

For tenants serving a global audience, the round-trip from a faraway
client to the primary region is the dominant page-load cost. With
read replicas in-region, the slow path is reduced to writes (which
are rarer) and the public theme is served entirely from edge.

For larger operators, multi-region is also the disaster-recovery
story: a complete primary-region failure is recoverable by promoting
a replica region.

## Config flags

All flags below live in the standard `packages/go/config` surface
and read from environment variables.

### Region identity

| Flag                   | Default       | Notes                                                                  |
| ---------------------- | ------------- | ---------------------------------------------------------------------- |
| `GONEXT_REGION`        | `""`          | Required in v2. Short slug, e.g. `us-east`, `eu-west`. Used in logs.   |
| `GONEXT_REGION_ROLE`   | `"primary"`   | One of `primary`, `replica`. Replicas refuse writes and surface 503.   |
| `GONEXT_REGION_PEERS`  | `""`          | Comma-separated peer regions. Used for cache fan-out (see below).      |

### Database read replicas

| Flag                       | Default | Notes                                                                                              |
| -------------------------- | ------- | -------------------------------------------------------------------------------------------------- |
| `DATABASE_URL`             | -       | Primary, read-write. Required everywhere.                                                          |
| `DATABASE_REPLICA_URL`     | `""`    | Optional. When set, read-only queries are routed here. Falls back to `DATABASE_URL` on error.      |
| `DATABASE_REPLICA_MAX_LAG` | `5s`    | Reads served from a replica that's lagged > this duration fall back to the primary.                |

In `GONEXT_REGION_ROLE=replica`, the API binary requires
`DATABASE_REPLICA_URL`; otherwise reads of regional data would cross
the WAN.

### Redis cluster mode

v1 uses a single Redis instance. v2 supports Redis Cluster for both
sessions (sharded by token) and the Asynq queue (Asynq itself does
not support cluster mode, so the queue Redis stays single-instance
in the primary region).

| Flag                  | Default            | Notes                                                                                       |
| --------------------- | ------------------ | ------------------------------------------------------------------------------------------- |
| `REDIS_URL`           | -                  | Cache + sessions. Optionally points at a cluster (`redis://host:6379?cluster=true`).        |
| `REDIS_JOB_URL`       | inherits           | Asynq queue. Always single-instance, always in the primary region.                          |
| `REDIS_CLUSTER_MODE`  | inferred from URL  | Force-override if the URL scheme is ambiguous.                                              |
| `REDIS_REPLICA_READS` | `true`             | Whether to send reads to cluster replicas. Off for strong consistency.                      |

### CDN headers

The public theme is served behind a CDN. Multi-region deployments
also serve the admin behind a CDN (cached `Vary: Cookie, Accept-Encoding`)
so the initial document load is fast.

| Header                | Set by                | Notes                                                                                  |
| --------------------- | --------------------- | -------------------------------------------------------------------------------------- |
| `Cache-Control`       | `apps/api`            | `public, max-age=60, s-maxage=300, stale-while-revalidate=86400` for theme reads.      |
| `Surrogate-Key`       | `apps/api`            | `post:<id> author:<id> tag:<slug>`. Drives the targeted-invalidate fan-out below.      |
| `Vary`                | `apps/api`            | `Accept, Accept-Encoding, Cookie` on theme; only `Accept-Encoding` on API JSON.        |
| `X-GoNext-Region`     | `apps/api`            | Region slug, for client-side debug.                                                    |

## Fan-out URLs

When an editor publishes a post or otherwise mutates cache-relevant
data, the API issues a **fan-out invalidation** to every peer
region's CDN.

### Targeted invalidate

```
POST https://cdn-<peer-region>.example.com/_invalidate
Content-Type: application/json

{
  "surrogate_keys": ["post:42", "author:7"],
  "issued_at": "2026-05-26T13:00:00Z",
  "from_region": "us-east"
}
```

- Each peer region acks within 2 s.
- Failed fan-outs are retried via the Asynq `webhook` queue (already
  shipped in v1).

### Bulk invalidate

For schema changes (theme reload, plugin install) that touch every
cached surface, a bulk wildcard:

```
POST https://cdn-<peer-region>.example.com/_invalidate
{ "surrogate_keys": ["*"], "from_region": "us-east" }
```

Used sparingly — clears every cache, expensive.

## Failover

Primary-region failure:

1. **Promote a replica.** Postgres replica is promoted to primary;
   `GONEXT_REGION_ROLE` is flipped to `primary` on that region's
   replicas.
2. **DNS cutover.** The write-path DNS record (`api-write.example.com`)
   is repointed at the new primary.
3. **Drain the queue Redis.** Asynq tasks queued in the old primary
   region are lost (the queue Redis is single-instance per design).
   Tenant-visible side effect: outbound webhooks pending at the
   moment of failover may be skipped. Document this in the
   compliance binder.
4. **Reverse the replication.** The old primary, when it returns,
   becomes a replica of the new primary.

## Open questions

- Cross-region session migration: today a session minted in
  `us-east` is invisible to `eu-west` because each region has its
  own Redis. The v2 cut needs either (a) global Redis-cluster
  with cross-region replication, accepting the latency penalty, or
  (b) signed session tokens that any region can validate without a
  store lookup. Decision deferred to ADR (TBD).
- Audit log replication: writes happen in the primary region, so
  the canonical `audit_log` table lives there. Replica regions read
  via the standard Postgres physical replication; no extra flags
  needed. Confirm SOC 2 auditor is comfortable with this.
- Media: today uploads land in object storage (S3-compatible) with
  cross-region replication enabled at the provider level. The API
  binary doesn't need to know — but the URL signing key must be
  the same across regions.
