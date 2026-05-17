-- 000007_permalinks.up.sql
--
-- Routing and historical-redirect storage.
--
-- Two tables land together because they form the two halves of the
-- request-time URL resolution chain documented in
-- docs/01-core-cms.md §7.3–§7.4 and docs/08-migration-compat.md §8.1:
--
--   1. `permalinks` — forward lookup for *live* routes. One row per
--      currently-served URL. PK is the normalized path, so the hot
--      path is a single index lookup:
--
--          SELECT post_id FROM permalinks WHERE path = $1;
--
--   2. `redirects` — historical and migration redirects. When the
--      forward lookup misses, the middleware falls back here:
--
--          SELECT to_path, code FROM redirects WHERE from_path = $1;
--
--      A single unified table services every source of redirect we
--      care about: slug changes on live content (doc 01 §7.4), manual
--      operator-authored redirects, WP-import generated redirects,
--      parsed `.htaccess` rewrites, and plugin-emitted redirects.
--      Per the contradictions-resolution review (doc 08 §8.1, "Fixed
--      per review C4 / contract M4"), doc 01's earlier proposal of a
--      separate `permalink_redirects` table was rejected in favour of
--      this single store.
--
-- Both tables depend on `posts` (migration 000004). No other schema
-- references them, so the order here is `permalinks` then `redirects`.

-- =============================================================================
-- permalinks — current forward lookup (live posts only)
-- =============================================================================
--
-- `path` is the normalized URL: leading slash, no trailing slash,
-- lowercased where the routing rules require. The TEXT primary key
-- keeps the lookup to a single B-tree probe — no pattern matching, no
-- regex, no parsing of `post_types.rewrite` at request time.
--
-- Path normalisation is the responsibility of the writer (the app-
-- level recompute helper from issue #77's acceptance criteria); the
-- DB stores whatever it's given and trusts that lookup queries
-- normalise the request path identically. We deliberately do *not*
-- enforce a CHECK constraint on `path` shape here because the rules
-- evolve (custom post types add new patterns) and a CHECK would
-- require a migration every time. The application layer owns the
-- contract.
--
-- `post_id` is `ON DELETE CASCADE` because a deleted post can have
-- no live URL — the row in `permalinks` is meaningless without its
-- post. The historical 301 lives in `redirects` and survives the
-- deletion independently.
--
-- `is_current` is included per doc 01 §7.4: a slug change can either
-- delete the old `permalinks` row or flip its flag to false. The
-- table stays narrow enough that flipping is cheap, and keeping the
-- row around for audit/debug is occasionally useful. The lookup
-- query in the hot path filters by PK only — `is_current` is
-- informational metadata for the bookkeeping layer.
--
-- `created_at` records when the row was minted; combined with
-- `is_current = FALSE` rows you get a complete history of when each
-- live URL took effect.
CREATE TABLE permalinks (
    path        TEXT        PRIMARY KEY,
    post_id     UUID        NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    is_current  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reverse-lookup index: "what's the URL of post X?" The PK already
-- handles the forward case (path → post_id). Without this index the
-- reverse lookup would scan the whole table; with it, generating a
-- post's canonical URL is O(log n).
--
-- A post can in principle have multiple `permalinks` rows (one
-- `is_current = TRUE`, plus zero or more `is_current = FALSE` audit
-- rows from past slug changes if the bookkeeping layer keeps them).
-- B-tree is appropriate; the cardinality is low so any index works.
CREATE INDEX permalinks_post_id_idx ON permalinks (post_id);

COMMENT ON TABLE permalinks IS
    'Forward URL→post_id lookup for live routes. See docs/01-core-cms.md §7.3.';
COMMENT ON COLUMN permalinks.path IS
    'Normalized URL path (leading slash, no trailing). Application owns normalization.';
COMMENT ON COLUMN permalinks.is_current IS
    'TRUE for the post''s active URL; FALSE rows are retained-for-audit historical bindings.';

-- =============================================================================
-- redirects — unified historical + migration redirect store
-- =============================================================================
--
-- The shape is the canonical contract from doc 08 §8.1, with two
-- task-specified refinements over the doc-08 DDL:
--
--   * The status column is named `code` here (matching the HTTP
--     vocabulary of the middleware and the issue spec for #77), and
--     carries a CHECK constraint enumerating the four redirect codes
--     we serve: 301, 302, 307, 308. Anything else is a bug, so
--     reject at the storage layer.
--
--   * The `from_path` uniqueness is *partial*: it excludes rows where
--     `source = 'migration'`. This is the unification compromise:
--     migration runs can leave historical traces in the table (one
--     row per run for the same old path, in case the user wants to
--     re-run an importer and keep a paper trail of past mappings),
--     but for every other source — slug changes, manual entries,
--     `.htaccess`, plugin emissions — there must be at most one row
--     per `from_path` so the middleware lookup is unambiguous.
--
-- Other columns track the doc-08 shape:
--
--   * `id`: UUID v7 PK so rows sort by insertion time without an
--     extra `created_at` index.
--   * `from_path`, `to_path`: text. `to_path` can be either a relative
--     path (`/blog/x`) or an absolute URL (`https://archive/...`);
--     the middleware doesn't care.
--   * `source`: one of the five enumerated string values. We use a
--     CHECK constraint rather than an ENUM type so plugins can be
--     surfaced through the same table without requiring a schema
--     migration to register a new source — but for now the set is
--     fixed at the five listed in doc 08 §8.1.
--   * `source_run`: optional uuid pointing to a migration run (the
--     `migration_map` table from doc 08 §3, landing in a later
--     migration). Nullable because non-migration sources don't have
--     a run id.
--   * `identity`: free-form per-source identifier (e.g. plugin slug
--     for `source = 'plugin'`, or rule label for `htaccess`). Note
--     this is a *text* column for routing/debug purposes; doc 08's
--     `identity boolean` drift-detection flag is a separate concept
--     and is not modelled here — if drift detection is later
--     required, add a column in a follow-up migration.
--   * `hits`, `last_hit_at`: usage counters incremented atomically by
--     the middleware. `bigint` for the counter because a busy
--     redirect on a migrated site can rack up millions over time.
--   * `created_at`: when the redirect was written.
CREATE TABLE redirects (
    id          UUID        PRIMARY KEY DEFAULT gen_uuid_v7(),
    from_path   TEXT        NOT NULL,
    to_path     TEXT        NOT NULL,
    code        SMALLINT    NOT NULL DEFAULT 301
                CHECK (code IN (301, 302, 307, 308)),
    source      TEXT        NOT NULL
                CHECK (source IN ('slug_change', 'manual', 'migration',
                                  'htaccess', 'plugin')),
    source_run  UUID,
    identity    TEXT,
    hits        BIGINT      NOT NULL DEFAULT 0,
    last_hit_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Partial unique on `from_path` for everything *except* migration
-- sources. This is the central invariant of the unified table:
--
--   * Non-migration redirects (slug_change, manual, htaccess, plugin)
--     are de-duplicated by `from_path`. Two slug changes that
--     happen to produce the same old path would collide here; the
--     writer is expected to update the existing row instead of
--     inserting a new one.
--
--   * Migration runs are exempt. The same old WP URL can appear in
--     multiple rows tagged `source = 'migration'` (one per importer
--     run), so operators have a full audit trail of how the mapping
--     evolved across re-imports. The serving middleware resolves
--     ties by ordering on `created_at DESC` and taking the most
--     recent.
CREATE UNIQUE INDEX redirects_from_path_uq
    ON redirects (from_path)
    WHERE source != 'migration';

-- Secondary indexes for operator/admin workflows:
--   * `to_path`: "find every redirect pointing to /foo" (used when
--     deleting a post and warning about incoming redirects).
--   * `source`: "show me every redirect created by the WP importer"
--     (used by the migration rollback path documented in doc 08 §9.4).
CREATE INDEX redirects_to_path_idx ON redirects (to_path);
CREATE INDEX redirects_source_idx  ON redirects (source);

COMMENT ON TABLE redirects IS
    'Unified historical/migration redirect store. See docs/08-migration-compat.md §8.1 and docs/01-core-cms.md §7.4.';
COMMENT ON COLUMN redirects.code IS
    'HTTP redirect status: 301 (permanent), 302 (temp), 307 (temp, preserve method), 308 (permanent, preserve method).';
COMMENT ON COLUMN redirects.source IS
    'Origin of the redirect; one of slug_change | manual | migration | htaccess | plugin.';
COMMENT ON COLUMN redirects.source_run IS
    'Migration run id for source=migration rows; nullable for every other source.';
COMMENT ON COLUMN redirects.identity IS
    'Free-form per-source identifier (plugin slug, htaccess rule label, etc.); nullable.';
COMMENT ON COLUMN redirects.hits IS
    'Atomically-incremented usage counter; populated by the serving middleware.';
