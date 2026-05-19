-- 000027_redirects.up.sql
--
-- `redirects` — explicit URL redirect rules administered through the
-- admin UI. The permalinks table (early init) handles canonical content
-- URLs; this table handles the "I moved /old-page to /new-page in 2019
-- and external links keep pointing at the old slug" long tail that
-- WordPress sites accumulate over years.
--
-- The middleware (packages/go/redirects) consults this table BEFORE
-- the renderer sees a request. A literal match short-circuits with a
-- 301/302/307/308 status; a regex match supports capture-group
-- substitution in the destination (`/blog/(.*) -> /posts/$1`).
--
-- Why a separate table rather than extending permalinks:
--   * Permalinks point at LIVE content (post_id, page_id, etc.) and
--     are mutated by content authoring. Redirects are operator-curated
--     rules that outlive the content they originally referenced.
--   * Permalinks are 1:1 with content rows; redirects are 1:N (many
--     legacy paths can collapse to one canonical destination).
--   * Hit-counting on permalinks would pollute the hot path; here
--     it's the whole point of the table.
--
-- This migration depends on:
--   * 000001_init  — `pgcrypto`, `gen_uuid_v7()`.
--   * 000002_users — `users(id)` for the `created_by` FK.

-- =============================================================================
-- redirects
-- =============================================================================

CREATE TABLE IF NOT EXISTS redirects (
    -- Surrogate primary key. UUID v7 keeps inserts B-tree-friendly
    -- (ADR 0003) and gives the admin UI a stable handle for edit/delete.
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- The path being matched. For literal rules this is the exact
    -- request path including leading slash ("/old-page"); for regex
    -- rules this is the regex pattern itself ("^/blog/(.*)$"). The
    -- engine compiles the pattern once at load time and re-uses it.
    --
    -- We store paths only (not full URLs) because host-level redirects
    -- belong at the reverse proxy layer; this table is for in-app
    -- redirects where the renderer would otherwise 404.
    source_path         TEXT NOT NULL CHECK (length(btrim(source_path)) > 0),

    -- Where the request is sent. For literal rules this is a path or
    -- absolute URL; for regex rules this can include `$1`/`$2` capture
    -- substitutions evaluated by the engine.
    destination_path    TEXT NOT NULL CHECK (length(btrim(destination_path)) > 0),

    -- HTTP status. 301 (Moved Permanently) is the default; 308 is the
    -- method-preserving variant; 302/307 cover temporary moves. We
    -- pin the set explicitly so the table doesn't accidentally become
    -- a vector for cache-poisoning 200s or 303 method changes.
    status              SMALLINT NOT NULL DEFAULT 301
                        CHECK (status IN (301, 302, 307, 308)),

    -- Regex matching toggle. When false, the engine looks the source
    -- up in a hashmap and returns immediately. When true, the engine
    -- iterates the regex set in `created_at` order — the first match
    -- wins. Regex rules have higher latency, so the UI nudges authors
    -- toward literal rules first.
    is_regex            BOOLEAN NOT NULL DEFAULT FALSE,

    -- Lifetime hit counter. Incremented by an atomic in the engine
    -- and flushed every 30s; an admin viewing "top traffic" sees
    -- recent activity within that window.
    hit_count           BIGINT NOT NULL DEFAULT 0,

    -- Wall-clock of the most recent match. NULL until first hit.
    -- Updated in the same 30s batch as hit_count.
    last_hit_at         TIMESTAMPTZ,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Issuing operator. SET NULL on user delete: deleting an operator
    -- shouldn't dissolve their redirect rules — the rule is policy,
    -- not personal data, and the audit log preserves the original
    -- author independently.
    created_by          UUID REFERENCES users(id) ON DELETE SET NULL
);

COMMENT ON TABLE  redirects IS
    'Operator-curated URL redirect rules. Consulted by middleware before the renderer; literal rules use a hashmap, regex rules iterate in creation order. Distinct from permalinks (#init) which point at live content.';
COMMENT ON COLUMN redirects.source_path      IS 'Request path to match (literal) or regex pattern (when is_regex). Leading slash required for literal rules.';
COMMENT ON COLUMN redirects.destination_path IS 'Target path or absolute URL. May reference $1/$2 capture groups when the rule is a regex.';
COMMENT ON COLUMN redirects.status           IS 'HTTP redirect status. 301/308 are permanent (cacheable); 302/307 are temporary. Pinned set; other values rejected.';
COMMENT ON COLUMN redirects.is_regex         IS 'False = hashmap lookup; True = pattern compiled at load and matched in creation order.';
COMMENT ON COLUMN redirects.hit_count        IS 'Lifetime match counter. Updated by the engine flusher every 30s, not per-request, to keep the hot path lock-free.';
COMMENT ON COLUMN redirects.last_hit_at      IS 'Wall-clock of the most recent match. NULL until first hit. Updated in the same 30s flush as hit_count.';
COMMENT ON COLUMN redirects.created_by       IS 'Issuing operator. SET NULL on user delete; the audit log retains the original author.';

-- =============================================================================
-- Constraints + Indexes
-- =============================================================================

-- A literal "/old-page" and a regex "/old-page" with the same pattern
-- text are different rules (the engine routes through different
-- match paths), so uniqueness is on the (source, is_regex) pair, not
-- source alone. Authors who try to create a duplicate literal rule get
-- a 409 from the admin UI translating the UNIQUE violation.
CREATE UNIQUE INDEX IF NOT EXISTS redirects_source_kind_uniq_idx
    ON redirects (source_path, is_regex);

-- Hot-path lookup index. The engine bulk-loads literal rules into an
-- in-process hashmap at boot, but admin queries ("show me all rules
-- for /blog/*") still hit Postgres. The composite (is_regex,
-- created_at) lets the admin list paginate by created_at within a
-- regex-vs-literal filter without sorting in memory.
CREATE INDEX IF NOT EXISTS redirects_kind_created_at_idx
    ON redirects (is_regex, created_at DESC);

-- "Top traffic" admin tab orders by hit_count DESC. Partial index on
-- hit_count > 0 so cold rules (never hit) don't take up space in the
-- index — the tab is explicitly about traffic, so unhit rows are
-- correctly absent.
CREATE INDEX IF NOT EXISTS redirects_hit_count_idx
    ON redirects (hit_count DESC)
    WHERE hit_count > 0;
