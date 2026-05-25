-- 000030_plugin_data_abi.up.sql
--
-- Persistent storage surface for the plugin host data ABIs:
--
--   * gn_db_read / gn_db_write   — per-plugin Postgres role + scoped
--     views catalog (issue #118).
--   * gn_kv_*                    — Redis-backed namespaced KV with a
--     quota table that lets the host evict the oldest keys when a
--     plugin overruns its declared budget (issue #146).
--   * gn_cache_invalidate        — transactional outbox the host writes
--     into; a tiny worker drains it into Redis pub/sub (issue #175).
--
-- All three ABIs are capability-gated (db.read, db.write,
-- cache.invalidate, kv.read, kv.write) and audit-emitted on write.
-- This migration is the schema half; the Go-side wiring lives in
-- packages/go/plugins/runtime/host_data.go and
-- packages/go/cache/invalidator.
--
-- 000013_outbox is the generic transactional outbox. We give cache
-- invalidation its own narrow table because the consumer shape is
-- different (small ordered rows, high churn, all rows get marked
-- consumed on drain rather than deleted), and mixing it with the
-- general-purpose outbox would force the outbox poller to learn
-- cache semantics.
--
-- ──────────────────────────────────────────────────────────────────
-- Plugin role catalog
-- ──────────────────────────────────────────────────────────────────
--
-- Each plugin gets one logical role (NAME == plugin slug) so the host
-- can `SET LOCAL ROLE plugin_<slug>` before issuing queries. We do NOT
-- create real Postgres ROLEs here — that's an out-of-transaction DDL
-- step the lifecycle Manager performs at activation time, gated by the
-- operator. This table is the host-side bookkeeping of "which role
-- name maps to which plugin"; the host consults it before swapping
-- roles and rejects calls whose plugin lacks an entry.

CREATE TABLE plugin_db_roles (
    plugin_slug   TEXT PRIMARY KEY,
    role_name     TEXT NOT NULL UNIQUE,

    -- Allowlist of relations the plugin may reference in db.read /
    -- db.write queries. NULL means "no restriction beyond the role's
    -- own GRANTs"; the host still enforces the keyword allowlist and
    -- single-statement rule.
    allowed_views TEXT[],

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE plugin_db_roles IS
    'Maps plugin slug to a Postgres role and an allowlist of relations '
    'the plugin may read/write. Populated by the lifecycle Manager at '
    'plugin activation; consulted by gn_db_read/gn_db_write before '
    'each query.';

-- ──────────────────────────────────────────────────────────────────
-- KV quota tracking
-- ──────────────────────────────────────────────────────────────────
--
-- Per-plugin counters: how many keys exist under `plugin:<slug>:` and
-- the cumulative byte size. The host increments on every gn_kv_set
-- and decrements on every gn_kv_del. On overflow against the
-- manifest-declared quota, the host soft-evicts the oldest keys.

CREATE TABLE plugin_kv_quotas (
    plugin_slug   TEXT PRIMARY KEY,

    -- max_bytes / max_keys are mirrored from the manifest at
    -- activation. NULL means "unlimited" (operator override).
    max_bytes     BIGINT,
    max_keys      INT,

    -- Cumulative usage. The host updates these inside the same
    -- transaction it inserts the key, so the counter never drifts.
    used_bytes    BIGINT NOT NULL DEFAULT 0,
    used_keys     INT NOT NULL DEFAULT 0,

    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE plugin_kv_quotas IS
    'Per-plugin KV quota and cumulative usage. Mirrors the manifest''s '
    'storage.kv block; the host enforces the budget before writing to '
    'Redis and soft-evicts the oldest key when a plugin overruns.';

-- The eviction path needs to know which key is oldest for a given
-- plugin. We track every key the host ever writes in a thin index
-- table; full values live in Redis.

CREATE TABLE plugin_kv_index (
    plugin_slug   TEXT NOT NULL REFERENCES plugin_kv_quotas(plugin_slug)
                       ON DELETE CASCADE,
    key           TEXT NOT NULL,
    size_bytes    INT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin_slug, key)
);

CREATE INDEX plugin_kv_index_evict_idx
    ON plugin_kv_index (plugin_slug, created_at);

COMMENT ON TABLE plugin_kv_index IS
    'Per-key bookkeeping (size + write time) used by the KV ABI to '
    'evict the oldest entries when a plugin overruns its quota.';

-- ──────────────────────────────────────────────────────────────────
-- Cache invalidation outbox
-- ──────────────────────────────────────────────────────────────────
--
-- gn_cache_invalidate writes one row per tag here, inside the
-- caller's transaction. A small worker (packages/go/cache/invalidator)
-- streams the rows out as Redis pub/sub notifications and marks them
-- consumed.

CREATE TABLE cache_invalidations (
    id            BIGSERIAL PRIMARY KEY,

    -- The plugin that emitted the invalidation. Auditable, and a
    -- defense-in-depth check: the worker re-prefixes the tag with the
    -- plugin slug before publishing.
    plugin_slug   TEXT NOT NULL,

    -- The tag the plugin wants invalidated. Stored UN-prefixed; the
    -- worker prefixes `plugin:<slug>:` on publish.
    tag           TEXT NOT NULL,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- consumed_at is non-NULL once the worker has published the row.
    consumed_at   TIMESTAMPTZ
);

CREATE INDEX cache_invalidations_unconsumed_idx
    ON cache_invalidations (id)
    WHERE consumed_at IS NULL;

COMMENT ON TABLE cache_invalidations IS
    'Transactional outbox for gn_cache_invalidate. Each row is one '
    '(plugin_slug, tag) pair the caller wants invalidated; a worker '
    'drains the table into Redis pub/sub. Rows are auto-namespaced by '
    'plugin_slug on publish.';
