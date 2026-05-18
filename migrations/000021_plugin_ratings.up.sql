-- 000021_plugin_ratings.up.sql
--
-- Marketplace data model — user ratings.
--
-- One row per (plugin_version, user). A user can rate the same plugin
-- multiple times only if they rate distinct versions; this is
-- deliberate — a rating left against v1.0 stays attached to v1.0 when
-- v2.0 ships, so the v1 rating doesn't drag down the v2 score.
--
-- The aggregate "score for this listing" is computed at read time by
-- joining ratings against versions and averaging. We don't denormalise
-- a cached aggregate column because (a) the write rate is low (humans
-- rate plugins, not bots), (b) the read pattern is interactive (the
-- marketplace card renders the live score), and (c) Postgres handles
-- the AVG(stars) query well with the listing-scoped composite index
-- below.
--
-- Depends on:
--   * 000019_plugin_versions — for the plugin_version_id FK target.
--   * 000002_users           — for the user_id FK target.

CREATE TABLE plugin_ratings (
    -- The version being rated. CASCADE because a rating outliving its
    -- version is meaningless.
    plugin_version_id   UUID NOT NULL
                        REFERENCES plugin_versions(id) ON DELETE CASCADE,

    -- The rater. ON DELETE CASCADE because GDPR-style "forget me"
    -- requests should take the user's authored content with them; the
    -- aggregate score loses one data point but no identifying trace
    -- remains.
    user_id             UUID NOT NULL
                        REFERENCES users(id) ON DELETE CASCADE,

    -- 1–5 stars. SMALLINT because the value never exceeds 5 and we
    -- save 2 bytes per row vs INTEGER. CHECK enforces the closed range
    -- at the database, not just the application layer — defence in
    -- depth against a malformed INSERT slipping past the handler.
    stars               SMALLINT NOT NULL
                        CHECK (stars BETWEEN 1 AND 5),

    -- Optional written review. Capped defensively at 8 KiB; the
    -- application layer enforces a tighter UI limit (1024 chars in v1)
    -- but the column ceiling is intentionally roomier so we don't have
    -- to migrate when the UI relaxes.
    review_text         TEXT
                        CHECK (review_text IS NULL OR length(review_text) <= 8192),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One rating per user per version. A user re-rating the same
    -- version UPSERTs over the existing row (see ratings.go:Submit).
    PRIMARY KEY (plugin_version_id, user_id)
);

COMMENT ON TABLE  plugin_ratings IS
    'User ratings, one per (version, user). Aggregates are computed at read time.';
COMMENT ON COLUMN plugin_ratings.stars IS
    '1..5. Enforced both here and in the application layer.';

-- "Show me the score for this version" — the join target for the
-- aggregate query in ratings.go:Aggregate. The (plugin_version_id,
-- stars) compound lets the planner index-only-scan when computing
-- AVG(stars) without touching the heap.
CREATE INDEX plugin_ratings_version_stars_idx
    ON plugin_ratings (plugin_version_id, stars);

-- "Show me everything this user has rated" — feeds the per-user
-- profile page and the moderation surface ("did this account rate
-- 50 plugins in 5 minutes?").
CREATE INDEX plugin_ratings_user_idx
    ON plugin_ratings (user_id, created_at DESC);
