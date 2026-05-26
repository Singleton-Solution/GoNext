-- 000037_media_text.up.sql
--
-- Storage for extracted full-text content of PDF (and other text-
-- bearing) media assets. Backs issue #60: the worker's
-- media.pdf.process task runs pdftotext on a freshly uploaded PDF
-- and stores the result here so the admin search index can target
-- the file's contents alongside the post bodies.
--
-- One row per media asset (1:1 — the media.id is the PK). The
-- relationship is enforced by the FK; deleting the parent media row
-- cascades through and frees the text row.
--
-- Design notes
--
--   * full_text is the verbatim extraction. We keep it (rather than
--     just the tsvector) so a future re-indexing pass with a
--     different language config can rebuild the tsvector from the
--     stored text without re-running pdftotext on the original PDF.
--
--   * content is a generated tsvector column. Storing it lets the
--     GIN index back the `media_text.content @@ tsquery` lookup
--     directly — no on-read coerce, no functional-index gotchas.
--
--   * extracted_at is wall-clock at the moment the worker wrote the
--     row. Used by the admin UI to surface "indexed 5 minutes ago"
--     and by a future re-extract trigger that wants to skip rows
--     that are already fresh.
--
-- Depends on:
--   * 000024_media — parent table.

CREATE TABLE media_text (
    -- 1:1 with media. Cascade because the text is a derivative
    -- artifact, not independent data — orphaning it would be a
    -- pure leak.
    media_id      UUID PRIMARY KEY
                  REFERENCES media(id) ON DELETE CASCADE,

    -- Raw extracted text. Capped at 16 MiB — a PDF this big is
    -- almost certainly a scanned image stack, and the text payload
    -- would dwarf the actual signal. Operators with larger PDFs
    -- can disable the cap on a per-deployment basis via the
    -- worker's PDF text size limit env var.
    full_text     TEXT NOT NULL DEFAULT ''
                  CHECK (length(full_text) <= 16 * 1024 * 1024),

    -- Generated full-text-search vector. STORED so the GIN index
    -- below covers it without a functional index dance.
    -- to_tsvector('simple', ...) — we use the 'simple' config
    -- because the documents are arbitrary user content where the
    -- language is unknown; pluggable per-deployment via a future
    -- options key.
    content       TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', full_text)) STORED,

    extracted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX media_text_content_idx
    ON media_text
    USING GIN (content);

CREATE INDEX media_text_extracted_at_idx
    ON media_text (extracted_at DESC);

COMMENT ON TABLE media_text IS
    'Extracted full-text content for media assets (PDF, etc). Written by the media.pdf.process worker task. See issue #60.';
