-- 000024_media.up.sql
--
-- Media library table — backs the operator-facing Media Library admin
-- UI (issue: media library). Stores ONE row per uploaded blob; variants
-- (resized renderings) belong to a follow-up table and are derived
-- on-demand by the libvips pipeline guarded by packages/go/media's
-- Coalescer.
--
-- Design notes
--
--   - The blob bytes live in S3 (or any S3-compatible store; the
--     storage.PathStyle config knob keeps MinIO/R2/Backblaze on the
--     same code path). Postgres only stores metadata + the S3 object
--     key, never the bytes themselves — a single image, even small,
--     is many orders of magnitude bigger than the row that describes
--     it, and Postgres is the wrong place to put them.
--
--   - sha256 is the content hash of the uploaded bytes. The upload
--     handler computes it streaming during the multipart read, then
--     does a UPSERT-style lookup: if a row already has that hash, we
--     skip the S3 PUT entirely and return the existing record. This
--     is the "dedupe" that the spec requires; it also defends against
--     an operator who uploads the same logo twice from two browsers
--     racing to allocate the same storage key.
--
--   - storage_key is the path the object lives at inside the bucket.
--     Format is "{prefix}/{yyyy}/{mm}/{ulid}-{slugified-filename}";
--     the application layer picks the format, the schema only enforces
--     uniqueness so an operator can locate the row from the URL and
--     never end up with two rows pointing at the same blob.
--
--   - uploader_id is a hard FK to users(id) — orphaning a media row
--     when the user is deleted would leave a blob with no admin
--     accountable for it. ON DELETE RESTRICT is intentional: a user
--     with media must be re-assigned (or the media re-assigned) before
--     they can be removed. The follow-up admin/users workflow will
--     surface this with a friendly UI; for now the FK is the seatbelt.
--
--   - deleted_at is a SOFT-DELETE marker. The bytes stay in S3 until a
--     purge cron sweeps them; the row stays in Postgres so a
--     mistakenly-deleted image can be undeleted by clearing this
--     column. The list endpoint filters out deleted rows by default;
--     a future "trash" view can flip the filter for recovery.
--
-- Depends on:
--   * 000001_init — pgcrypto/timestamptz/uuid baseline.
--   * 000002_users — uploader_id FK target.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE media (
    -- UUID primary key. Generated server-side via gen_random_uuid()
    -- because the surrogate id leaks into the public URL of the variant
    -- proxy and we don't want a sequential id revealing the upload
    -- order to anonymous clients.
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The original filename as supplied by the uploading client. This
    -- is human-facing only — operators recognise their files by name
    -- in the grid view. We do NOT use it to compute the storage key
    -- (the application layer slugifies + ULID-prefixes there); the
    -- two are decoupled so a rename of one doesn't churn the other.
    --
    -- Capped to 255 chars because POSIX filesystems on the operator's
    -- side typically cap at 255 and we don't want to surface a filename
    -- that round-trips badly if the operator downloads it. The CHECK
    -- enforces non-empty so a malformed multipart upload (which would
    -- produce filename="") is rejected by the schema as well as the
    -- handler — defence in depth.
    filename        TEXT NOT NULL
                    CHECK (length(filename) > 0 AND length(filename) <= 255),

    -- The sniffed MIME type. We deliberately store the SNIFFED value
    -- (http.DetectContentType on the first 512 bytes) rather than the
    -- client-supplied Content-Type, because the client header is
    -- unauthenticated and an attacker can lie about it. The handler
    -- rejects executable types (application/x-msdownload, x-sh, etc.)
    -- before INSERT, so anything that lands here is the result of a
    -- successful sniff and is the canonical mime for the bytes.
    mime_type       TEXT NOT NULL
                    CHECK (length(mime_type) > 0 AND length(mime_type) <= 128),

    -- Total byte length. BIGINT because a single video upload can be
    -- multi-gigabyte; the handler's MaxBytesReader (50 MiB cap) is the
    -- guard against pathological inputs, but the column should accept
    -- whatever the operator configured the limit to be.
    byte_size       BIGINT NOT NULL CHECK (byte_size > 0),

    -- Image dimensions. NULL for non-image media (audio, PDF, video
    -- before the probe lands); the application layer fills them in
    -- via libvips for images during upload. Storing as INT (not
    -- SMALLINT) because a panorama image legitimately exceeds 32k px.
    width           INT  CHECK (width  IS NULL OR width  > 0),
    height          INT  CHECK (height IS NULL OR height > 0),

    -- Alt text — the accessibility-critical description shown to
    -- screen readers and as fallback when the image fails to load.
    -- Empty string is allowed (an empty alt is the correct value for
    -- decorative images per WCAG); we do NOT use NULL to distinguish
    -- "no alt was set" from "alt is intentionally empty", because the
    -- two are operationally the same and the column has a default of
    -- empty string so a freshly inserted row is well-formed.
    alt_text        TEXT NOT NULL DEFAULT ''
                    CHECK (length(alt_text) <= 2048),

    -- Caption — the longer human-readable description rendered next
    -- to the asset in the public theme. Optional; default empty.
    caption         TEXT NOT NULL DEFAULT ''
                    CHECK (length(caption) <= 4096),

    -- S3 object key. UNIQUE so the same blob can't end up at two
    -- rows (the application layer also dedupes by sha256; this is
    -- the storage-side guard for the case where two clients race
    -- onto the same key, which only happens if the key generation is
    -- buggy — but if it is, we want the constraint to surface it).
    storage_key     TEXT NOT NULL UNIQUE
                    CHECK (length(storage_key) > 0 AND length(storage_key) <= 1024),

    -- SHA-256 content hash. 32 bytes; stored as BYTEA so equality is
    -- a fixed-length compare. UNIQUE so dedupe is a single-statement
    -- INSERT ... ON CONFLICT lookup rather than a SELECT-then-INSERT
    -- race-prone two-step.
    sha256          BYTEA NOT NULL UNIQUE
                    CHECK (length(sha256) = 32),

    -- The user who uploaded the blob. RESTRICT (rather than CASCADE)
    -- because removing a user shouldn't quietly orphan their uploads;
    -- the deletion path must explicitly re-assign or remove the media.
    uploader_id     UUID NOT NULL
                    REFERENCES users(id) ON DELETE RESTRICT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Soft-delete tombstone. NULL means "active"; non-NULL is the
    -- moment the operator pressed Delete in the admin UI. The purge
    -- cron sweeps rows with deleted_at < now() - retention and
    -- removes both the S3 object and the row.
    deleted_at      TIMESTAMPTZ
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- "My uploads" — the per-user view in the admin UI. The DESC ordering
-- matches the grid's newest-first sort; without it the planner falls
-- back to a sort step on each page request.
CREATE INDEX media_uploader_created_idx
    ON media (uploader_id, created_at DESC)
    WHERE deleted_at IS NULL;

-- Filter-by-type — the grid's "Images / Documents / Video" chips
-- become a `mime_type LIKE 'image/%'` predicate; pre-ordering by
-- created_at makes the chip-filtered list paginatable without a sort.
-- Active rows only — the trash view is a separate, infrequent query
-- and doesn't need this index.
CREATE INDEX media_mime_created_idx
    ON media (mime_type, created_at DESC)
    WHERE deleted_at IS NULL;

-- Sweep support for the purge cron. The purge runs:
--   DELETE FROM media WHERE deleted_at IS NOT NULL
--                       AND deleted_at < now() - <retention>
--   LIMIT N;
-- Without this index the sweep degrades to a sequential scan over the
-- whole table, which on a media-heavy site (millions of rows) will
-- starve the prod workload. Partial-on so the index only holds soft-
-- deleted rows; the live grid never has to skip past them.
CREATE INDEX media_deleted_at_idx
    ON media (deleted_at)
    WHERE deleted_at IS NOT NULL;
