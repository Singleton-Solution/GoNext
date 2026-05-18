-- 000018_rum_events.up.sql
--
-- Real User Monitoring (RUM) raw event store. Stores Core Web Vitals
-- and custom timings emitted by visitors to the public theme via the
-- POST /_/rum/beacon endpoint. Issue #132.
--
-- Why a single wide-ish table and not per-metric tables?
--
--   - The percentile aggregation query is "give me p50/p75/p95 of
--     <metric> on <path> over <period>". With one row per (metric,
--     ts, value) we can serve every variant from one composite index;
--     a per-metric table would force the admin handler to switch on
--     metric and complicates retention.
--   - Volume is modest. A small site emits a handful of events per
--     pageview; a large site is the case where the operator should
--     swap the table for a partitioned hypertable (TimescaleDB) or
--     ship raw events to an external store. Both substitutions land
--     behind the same query.go interface, so the upgrade is local.
--   - The schema is append-only. We never UPDATE a row; retention is
--     driven by a TTL via expires_at + a periodic vacuum job rather
--     than partitioning, because partition swaps would force a
--     migration-on-every-day operator overhead that isn't worth it at
--     this volume tier.
--
-- PII posture:
--
--   - session_id is a CLIENT-side opaque token (random; not derived
--     from the user record) the beacon library generates per
--     visit. The server never resolves it to a user, never joins it
--     against the users table, and never logs it next to an IP. It is
--     stored so the operator can answer "how many unique sessions
--     contributed to this percentile" without a head count of IPs.
--   - country is OPTIONAL and capped to a 2-letter ISO code; the
--     beacon endpoint resolves it from the request IP when the
--     operator opts in (CDN edge headers or MaxMind), and stores
--     NULL otherwise. We deliberately store country, not city, to
--     keep the geo grain coarse.
--   - conn is OPTIONAL — the NetworkInformation API value reported by
--     the client (4g, 3g, slow-2g, etc.). Free-form lowercase string
--     up to 16 chars; NULL when the browser doesn't expose it.
--
-- Depends on:
--   * 000001_init — for the pgcrypto/timestamptz baseline. We do not
--     join to users — the table is intentionally PII-free at this tier.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE rum_events (
    -- BIGSERIAL because volume can grow to millions on a busy site
    -- and INT would wrap. Auto-incrementing is fine here — the ID is
    -- internal; the natural key for an event is (ts, session_id,
    -- metric) and we don't expose the row id outside the API.
    id              BIGSERIAL PRIMARY KEY,

    -- Server-side ingestion timestamp. The browser also sends a
    -- "since pageload" delta but we deliberately store the server
    -- clock to keep the time axis monotonic across visitors with
    -- skewed clocks. The trade is up to ~1 RTT of jitter, which is
    -- well inside the percentile bucket width we render.
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The visited URL path (e.g. "/", "/blog/foo/"). Stored verbatim
    -- from the beacon — the beacon library normalises the path to
    -- pathname only (no query, no fragment) before sending, so we
    -- don't have to worry about per-visitor query-param explosion
    -- defeating the index. Capped to 2 KiB to bound the worst case.
    page_path       TEXT NOT NULL
                    CHECK (length(page_path) > 0 AND length(page_path) <= 2048),

    -- Metric name. The five Core Web Vitals (LCP, INP, CLS, TTFB,
    -- FCP) are the canonical set; we leave the column open to TEXT
    -- so a future custom-timing extension (operator-defined marks)
    -- can land without a migration. The application layer is the
    -- gatekeeper here.
    metric          TEXT NOT NULL
                    CHECK (length(metric) > 0 AND length(metric) <= 32),

    -- The metric value. DOUBLE PRECISION because CLS is fractional
    -- (0.083, …) while LCP/INP/TTFB/FCP are millisecond integers but
    -- can include sub-millisecond fractions via the performance API.
    -- A single column type avoids per-metric branching downstream.
    value           DOUBLE PRECISION NOT NULL,

    -- web-vitals' bucketed rating: "good" | "needs-improvement" |
    -- "poor". The CHECK constraint is the contract — the beacon
    -- handler rejects unknown values before INSERT. Storing the
    -- pre-bucketed rating lets the admin page render a stacked bar
    -- without re-deriving thresholds (which differ per metric).
    rating          TEXT NOT NULL
                    CHECK (rating IN ('good', 'needs-improvement', 'poor')),

    -- The client-supplied session token. Hashed client-side (see
    -- packages/ts/rum-beacon/src/index.ts) so the server never sees
    -- a raw browser-generated identifier. Capped to 64 chars (a hex
    -- SHA-256 fits in that envelope, with headroom).
    session_id      TEXT NOT NULL
                    CHECK (length(session_id) > 0 AND length(session_id) <= 64),

    -- Optional ISO 3166-1 alpha-2 country code. NULL when the
    -- operator hasn't enabled geo enrichment or the lookup failed.
    -- Length 2 is the canonical form; an extra char of headroom
    -- avoids edge cases like "ZZ" sentinel codes some CDNs emit.
    country         TEXT
                    CHECK (country IS NULL OR length(country) <= 3),

    -- Optional connection class, free-form lowercase
    -- ("4g", "3g", "slow-2g", "wifi"). NULL when the browser does
    -- not expose NetworkInformation.
    conn            TEXT
                    CHECK (conn IS NULL OR length(conn) <= 16),

    -- Retention horizon. Defaults to ts + 30d; a periodic cleanup
    -- job (or a manual VACUUM) deletes rows past expires_at. We
    -- keep this as a column rather than computing on read so a
    -- future per-row TTL (e.g. high-cardinality custom metrics
    -- kept for shorter windows) doesn't require another migration.
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '30 days')
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- Primary read path is the percentile query: filter by metric +
-- page_path + ts range. The compound (metric, ts) index serves the
-- "all paths" case and seeds the planner for the path-filtered case
-- (which then applies the page_path predicate as a residual). We
-- chose this over a (metric, page_path, ts) tuple because the
-- cardinality of metric is ~5 while page_path can grow into the
-- thousands — the planner is happier picking a narrow leading column.
CREATE INDEX rum_events_metric_ts_idx
    ON rum_events (metric, ts DESC);

-- Path-filtered query support — admins commonly want "top slowest
-- routes" which scans by page_path. Partial-no since page_path is
-- mandatory.
CREATE INDEX rum_events_page_path_ts_idx
    ON rum_events (page_path, ts DESC);

-- Retention sweep. The cleanup job runs "DELETE FROM rum_events
-- WHERE expires_at < now() LIMIT N" in batches; without this index
-- the sweep degrades to a sequential scan over the whole table.
CREATE INDEX rum_events_expires_at_idx
    ON rum_events (expires_at);
