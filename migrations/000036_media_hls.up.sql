-- 000036_media_hls.up.sql
--
-- Adds the HLS playlist URL column to the media table — backs the
-- video transcoding pipeline (issue #52). When the worker's
-- media.video.transcode task lands, it writes the resulting
-- index.m3u8 URL here so the public player can pick HLS over the
-- raw mp4 source.
--
-- Design notes
--
--   * NULLABLE on purpose. Every existing video row predates the
--     pipeline and the playlist won't exist for them until an
--     operator triggers a reprocess; nullable means "not yet
--     transcoded" without a sentinel value. New uploads also flow
--     through nullable: the row is committed at upload-time, the
--     HLS URL fills in asynchronously once the worker finishes.
--
--   * TEXT (not bytea / json) because the URL is exactly what the
--     <video src=> attribute consumes; serialising or compressing
--     it would add overhead on the read hot-path.
--
--   * No FK to a playlist-segment table. The HLS output is many
--     small files (index.m3u8 + 6-second .ts segments) — tracking
--     each one as a row would blow up the table for no read-side
--     benefit. The bucket itself is the authoritative manifest;
--     this column points at the playlist that knows how to walk
--     them.
--
-- Depends on:
--   * 000024_media — the media table this column belongs to.

ALTER TABLE media
    ADD COLUMN hls_url TEXT
        CHECK (hls_url IS NULL OR length(hls_url) <= 2048);

COMMENT ON COLUMN media.hls_url IS
    'Public URL of the HLS index.m3u8 produced by the media.video.transcode task. NULL until the worker writes it.';
