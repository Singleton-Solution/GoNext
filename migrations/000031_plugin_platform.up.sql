-- 000031_plugin_platform.up.sql
--
-- Storage for the plugin host's platform ABI surfaces: per-plugin
-- secrets (gn_secrets_get) and per-plugin cron schedules
-- (gn_cron_register). Both back exports defined in
-- packages/go/plugins/runtime/host_platform.go.
--
-- ──────────────────────────────────────────────────────────────────
-- plugin_secrets
-- ──────────────────────────────────────────────────────────────────
--
-- Each plugin has its own DEK (data encryption key) wrapping
-- per-secret blobs. The DEK is generated at plugin install and
-- stored elsewhere (encrypted-under-KEK in the plugin row); this
-- table only stores the encrypted secret values themselves.
--
-- Each secret row carries:
--
--   * plugin_slug — owning plugin
--   * key         — opaque caller-supplied identifier
--   * ciphertext  — AES-256-GCM ciphertext, includes the auth tag
--   * nonce       — 12-byte GCM nonce (random, per-secret)
--   * aad         — additional authenticated data (the slug + key,
--                   so a swap between plugins/keys fails decrypt)
--   * created_at  — wall-clock at write
--
-- (plugin_slug, key) is unique: gn_secrets_get(key) resolves at most
-- one row per plugin. Writes are NOT exposed to plugins — the WASM
-- ABI is read-only, per issue #114. Operators populate the table via
-- a separate admin path that holds the KEK.

CREATE TABLE plugin_secrets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin_slug  TEXT NOT NULL
                 CHECK (length(plugin_slug) > 0 AND length(plugin_slug) <= 128),
    key          TEXT NOT NULL
                 CHECK (length(key) > 0 AND length(key) <= 256),
    ciphertext   BYTEA NOT NULL,
    nonce        BYTEA NOT NULL
                 CHECK (length(nonce) = 12),
    aad          BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (plugin_slug, key)
);

CREATE INDEX plugin_secrets_slug_idx ON plugin_secrets (plugin_slug);

COMMENT ON TABLE plugin_secrets IS
    'Encrypted plugin secret blobs (AES-256-GCM, DEK-wrapped). Read-only from WASM via gn_secrets_get; see packages/go/plugins/runtime/host_platform.go.';

-- ──────────────────────────────────────────────────────────────────
-- plugin_cron_schedules
-- ──────────────────────────────────────────────────────────────────
--
-- Persistent record of every schedule registered by a plugin at
-- activation. The leader-elected scheduler in packages/go/jobs/cron
-- fires each enabled row; the fire dispatches through the hook bus
-- to the plugin's WASM handler.
--
-- enabled lets the activation gate flip a schedule off without
-- deleting the row (deactivation tombstone). When the plugin is
-- reactivated, we flip enabled back to TRUE rather than re-creating
-- the row, so the (slug, handler_id) identity stays stable across
-- activation cycles.

CREATE TABLE plugin_cron_schedules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin_slug  TEXT NOT NULL
                 CHECK (length(plugin_slug) > 0 AND length(plugin_slug) <= 128),
    schedule     TEXT NOT NULL
                 CHECK (length(schedule) > 0 AND length(schedule) <= 128),
    handler_id   TEXT NOT NULL
                 CHECK (length(handler_id) > 0 AND length(handler_id) <= 128),
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (plugin_slug, handler_id)
);

CREATE INDEX plugin_cron_schedules_enabled_idx
    ON plugin_cron_schedules (plugin_slug)
    WHERE enabled = TRUE;

COMMENT ON TABLE plugin_cron_schedules IS
    'Plugin-registered cron schedules dispatched through the hook bus. Fired by the leader-elected scheduler in packages/go/jobs/cron.';
