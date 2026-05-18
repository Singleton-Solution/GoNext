-- 000019_plugin_versions.up.sql
--
-- Marketplace data model — versions.
--
-- A listing has one or more versions. The version row is the durable
-- record of an individual artefact upload: the wasm binary's SHA-256
-- digest, the manifest blob that travelled with it, and the optional
-- detached signature that proves provenance.
--
-- The wasm bytes themselves are NOT stored in this table. Production
-- deployments push artefacts to object storage (see the media package
-- + S3/Minio integration) and key the upload by the SHA-256 digest in
-- `wasm_sha256` — the digest is the artefact's content address. Storing
-- multi-megabyte BYTEAs in a relational table is the wrong shape for
-- both rep and query patterns; the digest gives us integrity and
-- deduplication for free.
--
-- Depends on:
--   * 000018_plugin_listings — for the listing_id FK target.

CREATE TABLE plugin_versions (
    -- UUID v7 PK. Versions are time-sortable in their own right (newer
    -- versions sort after older ones) which lines up with the v7
    -- ordering, but the (listing_id, version) UNIQUE below is the
    -- natural key callers join on.
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- The listing this version belongs to. ON DELETE CASCADE because a
    -- listing's versions are a tightly-bound child collection — there
    -- is no meaningful "orphan version" state.
    listing_id      UUID NOT NULL
                    REFERENCES plugin_listings(id) ON DELETE CASCADE,

    -- Semantic version string ("1.4.2", "2.0.0-beta.1"). Stored as text
    -- because semver's grammar is richer than any single numeric type
    -- can express; comparison is done at the application layer using
    -- the standard semver library.
    version         TEXT NOT NULL
                    CHECK (length(version) > 0 AND length(version) <= 64),

    -- SHA-256 digest of the wasm artefact, 32 raw bytes. BYTEA (not
    -- TEXT) so equality checks are byte-exact and the row uses 32 + 4
    -- bytes of storage rather than 64 + 4 for hex. The Go store
    -- computes the digest from the supplied bytes — see Publish in
    -- versions.go.
    wasm_sha256     BYTEA NOT NULL
                    CHECK (octet_length(wasm_sha256) = 32),

    -- Manifest blob exactly as parsed at publish time. JSONB so we can
    -- query it later ("show me every version that declares the `kv`
    -- capability") without re-parsing. Default '{}'::JSONB rather than
    -- NULL so the column is always projectable.
    manifest        JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Optional detached signature, hex-encoded. NULL when the publisher
    -- didn't sign the artefact (allowed in v1; the marketplace UI will
    -- surface a "signed by X" badge only when this column is non-null).
    -- TEXT rather than BYTEA because hex is what the manifest carries
    -- and what verifier callers want to see in logs.
    signature_hex   TEXT,

    published_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- When the publisher (or platform) marked this version as
    -- deprecated. NULL = current. Deprecated versions remain
    -- installable for compat reasons but are surfaced with a banner
    -- in the catalogue.
    deprecated_at   TIMESTAMPTZ,

    -- A listing cannot publish the same version string twice. This is
    -- both a publisher-facing invariant ("you already shipped 1.4.2")
    -- and the join key the compat matrix table uses.
    UNIQUE (listing_id, version)
);

COMMENT ON TABLE  plugin_versions IS
    'One row per published artefact. Owns the integrity digest and manifest, not the wasm bytes themselves.';
COMMENT ON COLUMN plugin_versions.wasm_sha256   IS 'SHA-256 of the wasm artefact. Content-addresses the upload in object storage.';
COMMENT ON COLUMN plugin_versions.signature_hex IS 'Hex-encoded detached signature, optional. Absence means unsigned, not invalid.';
COMMENT ON COLUMN plugin_versions.deprecated_at IS 'NULL = current. Deprecated versions remain installable but flagged in the catalogue.';

-- "Show me every version of listing X" — the dominant read pattern,
-- ordered by published_at DESC so the most recent release is the first
-- row. Compound (listing_id, published_at) so the order is index-served.
CREATE INDEX plugin_versions_listing_published_idx
    ON plugin_versions (listing_id, published_at DESC);

-- Reverse lookup by content hash: "is this exact artefact already
-- published anywhere?" Used by the dedupe check in Publish.
CREATE INDEX plugin_versions_sha256_idx
    ON plugin_versions (wasm_sha256);
