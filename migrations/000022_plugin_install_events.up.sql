-- 000022_plugin_install_events.up.sql
--
-- Marketplace data model — install events.
--
-- Append-only telemetry. Every time a host installs, activates,
-- uninstalls, or errors on a plugin from the marketplace, one row
-- lands here. The marketplace UI's "most popular this week" carousel
-- reads from this table; the publisher dashboard's per-version error
-- rate reads from this table; the moderation surface's "is this
-- plugin causing widespread errors?" check reads from this table.
--
-- Design choices:
--
--   * BIGSERIAL PK rather than UUID v7. Events are append-only with
--     a single writer per host; the auto-increment serial gives us
--     monotonic ordering for free (UUID v7 would too but the BIGINT
--     comparison is cheaper for the aggregate queries that run over
--     millions of rows). We don't need cross-host ordering because
--     time-window queries are bucketed by created_at.
--
--   * host_id is plain TEXT, not a UUID FK. Hosts are external
--     installations that may or may not be registered in the GoNext
--     database — a self-hosted CMS pinging telemetry doesn't have a
--     row in our `users` or `hosts` table. The application layer
--     hashes the host's signature into a stable opaque string before
--     insert so the column is privacy-respecting by construction.
--
--   * No UPDATE / DELETE access path. The Go store only exposes
--     RecordInstallEvent (append) and aggregate read queries. Append-
--     only lets us partition by created_at later (the marketplace
--     telemetry pipeline target is >10M rows/month).
--
-- Depends on:
--   * 000018_plugin_listings — for the listing_id FK target.
--   * 000019_plugin_versions — for the version_id FK target.

CREATE TABLE plugin_install_events (
    -- Monotonic event ID. BIGSERIAL fits 9.2e18 events; if we ever hit
    -- that ceiling the table has been wildly successful and rotating
    -- the underlying sequence is a deployment problem, not a schema one.
    id              BIGSERIAL PRIMARY KEY,

    -- The listing. NULLable + ON DELETE SET NULL: when a listing is
    -- hard-deleted (rare; the usual path is delisted/banned), the
    -- historical events are preserved for moderation forensics with
    -- the FK nulled out.
    listing_id      UUID
                    REFERENCES plugin_listings(id) ON DELETE SET NULL,

    -- The version. Same NULL-on-delete treatment as listing_id, for
    -- the same reason: an event is a permanent record, not a join row.
    version_id      UUID
                    REFERENCES plugin_versions(id) ON DELETE SET NULL,

    -- Opaque host identifier (see note above). Required — an event
    -- with no host attribution is not useful for any of the downstream
    -- analytics, and rejecting at insert time keeps the noise floor
    -- low. CHECK so SQL-direct inserts can't bypass.
    host_id         TEXT NOT NULL
                    CHECK (length(host_id) > 0 AND length(host_id) <= 128),

    -- Event taxonomy:
    --   installed    — bundle landed, plugin row created.
    --   activated    — first transition to State=Active.
    --   uninstalled  — operator-driven removal.
    --   errored      — runtime parked the plugin in Errored.
    -- New event types may be added later; the CHECK is widened in a
    -- separate forward migration (per the README's expand/contract
    -- rule).
    event_type      TEXT NOT NULL
                    CHECK (event_type IN ('installed','activated','uninstalled','errored')),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE  plugin_install_events IS
    'Append-only telemetry. Feeds popularity ranking and per-version error rate.';
COMMENT ON COLUMN plugin_install_events.host_id IS
    'Opaque host identifier — hashed signature, not a directly identifying value.';
COMMENT ON COLUMN plugin_install_events.event_type IS
    'installed | activated | uninstalled | errored. Extended via forward migration only.';

-- "Top plugins this week" — feeds the popularity carousel. The
-- (listing_id, created_at) compound supports the natural query
-- shape: GROUP BY listing_id, COUNT(*) WHERE created_at > now() - 7d.
CREATE INDEX plugin_install_events_listing_created_idx
    ON plugin_install_events (listing_id, created_at DESC);

-- "Error rate for this version" — feeds the publisher dashboard.
-- Partial index over only the errored rows because the read pattern
-- is "count errors", not "count all events", and the partial form is
-- 10x smaller in steady state.
CREATE INDEX plugin_install_events_version_errored_idx
    ON plugin_install_events (version_id, created_at DESC)
    WHERE event_type = 'errored';

-- "Recent activity for this host" — feeds the moderation surface.
-- A host installing 100 plugins in 10 seconds is a signal worth
-- surfacing.
CREATE INDEX plugin_install_events_host_created_idx
    ON plugin_install_events (host_id, created_at DESC);
