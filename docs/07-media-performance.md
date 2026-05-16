# 07 — Media & Performance

> **Status:** design. **Owner:** media/perf agent. **Depends on:** `00-architecture-overview.md`, `01-core-cms.md` (for the attachment relationship), `03-theme-system.md` (for the `Image` helper contract), `05-admin-api.md` (for the upload endpoints).
>
> **Audience:** a senior engineer who has lived with WordPress's `wp-content/uploads`, has regenerated thumbnails one time too many, knows what an LCP regression looks like at 3am, and is tired of paying for a CDN that ships 4MB JPEGs.
>
> This document owns two adjacent concerns: **media handling** (upload, storage, transforms, serving) and **the cross-cutting performance story** (caching layers, ISR, edge, RUM, budgets). They share a doc because in practice the media pipeline is half of the perf story — get media wrong and the rest doesn't matter.

---

## 0. Why this is a separate document

WordPress's media handling is its single most legacy-bound subsystem. The defaults haven't materially changed in a decade:

- Files land on the local filesystem under `wp-content/uploads/YYYY/MM/`. Multi-server installs need NFS/S3-Offload plugins.
- Image sizes are declared by the **theme** at runtime, so switching themes leaves you needing the "Regenerate Thumbnails" plugin.
- There is no native concept of folders or collections.
- Variants are eagerly generated at upload time only — change the size definitions later and existing media is out of step.
- The pipeline is single-threaded PHP-GD/ImageMagick on the request critical path, then later moved off via plugins.
- WebP arrived late, AVIF is plugin territory, and HLS for video is "you're on your own."

We are building on Go, S3, and a JS frontend that natively understands `<picture>` and `srcset`. We can be dramatically better with reasonable effort. This document specifies how.

On the performance side, WordPress's caching story is a stack of bolt-ons (W3 Total Cache, WP Super Cache, LiteSpeed, object-cache.php drop-ins, Cloudflare APO, fragment caches via transients...). Each works, but the whole has been organic, not designed. Our caching is a designed system with a clear invalidation model and tagged entries.

---

## 1. Goals & Non-Goals

### Goals (v1)

1. **Upload feels instant**, even for large files. No 30s spinner; no surprise size limits.
2. **No "regenerate thumbnails" step, ever.** Variants are generated on demand and cached forever; theme changes don't break old media.
3. **Modern formats by default**: AVIF/WebP served via content negotiation; fallback JPEG/PNG.
4. **Folders/collections** native to the data model.
5. **Video transcoding** to HLS without manual ffmpeg invocation.
6. **Sub-second LCP** for cached pages, sub-200ms TTFB at the CDN edge, with no third-party perf SaaS required to debug.
7. **A tag-based cache invalidation** model that is correct (no stale comments under a post you just edited) and fast (single mutation triggers a bounded blast radius).
8. **A perf budget** enforced in CI so a theme or plugin can't quietly ship 800KB of JS.

### Non-Goals (v1)

- A full DAM (digital asset management) suite: versioning, approvals, rights management. Folders + alt-text + basic metadata are enough.
- Live video streaming. Pre-recorded VOD only.
- Image AI editing (background removal, generative fill). A plugin can add this.
- Distributed S3 (multi-region writes). Single region for the bucket; CDN handles read distribution.
- Bandwidth caps / quota enforcement in v1. The plumbing exists (per-tenant metrics), the policy is later.

---

## 2. High-level shape

```
                              ┌──────────────────────────┐
                              │   Admin / Authoring UI   │
                              │   (Next.js)              │
                              └────┬──────────┬──────────┘
                                   │          │
              presigned PUT/POST   │          │  REST: /media (CRUD, list, search)
                                   ▼          ▼
   ┌───────────────────┐    ┌─────────────────────────────────────┐
   │   S3-compatible   │◄──►│           Go API Server             │
   │   object store    │    │  ┌────────┐  ┌────────────────────┐ │
   │  (originals +     │    │  │ Media  │  │  Job dispatcher    │ │
   │   variants)       │    │  │ svc    │  │  (Asynq)           │ │
   └────────┬──────────┘    │  └────────┘  └────────────────────┘ │
            │               │  ┌────────────────────────────────┐ │
            │               │  │  /img/{id}/{spec}  (image      │ │
            │               │  │  proxy: libvips, cache to S3)  │ │
            │               │  └────────────────────────────────┘ │
            │               └─────────────────┬───────────────────┘
            │                                 │
            │                                 ▼
            │                        ┌────────────────┐
            │                        │   Postgres     │
            │                        │  media,        │
            │                        │  variants,     │
            │                        │  collections   │
            │                        └────────────────┘
            │                                 ▲
            │                                 │ async jobs:
            │                                 │   - eager variants
            │                                 │   - video transcode
            │                                 │   - virus scan
            │                                 │   - text extract
            ▼                                 │
   ┌──────────────────────────────────────────┴──────────┐
   │                  Cloudflare CDN                     │
   │  edge cache: variants, /img/* responses,            │
   │  HLS segments, public site pages (via ISR)          │
   └─────────────────────────────────────────────────────┘
```

Three things to note up-front:

1. **Originals and variants live in the same bucket** under different key prefixes. The browser never reads originals directly for image rendering — it goes through `/img/{id}/{spec}`. (Documents and video are served directly.)
2. **The image proxy is part of the Go API server** in v1, not a separate service. We'll split it out when its load justifies horizontal scaling separately, which will be obvious from metrics.
3. **The CDN does most of the work.** The proxy is the cache miss path. We aim for a 95%+ hit ratio on image URLs in steady state.

---

## PART A — MEDIA

## 3. Upload Flow

We support three upload paths, picked by the client based on file size and capability detection.

### 3.1 Direct-to-S3 with presigned URLs (the default)

For files larger than ~5MB or images that don't need server-side validation, the browser uploads directly to S3 without proxying through the API server. This is the difference between a 50MB upload taking ~5 seconds on good fibre vs locking up an API worker for 30 seconds.

```
 Browser                       Go API                     S3
   │                             │                         │
   │   POST /media/uploads       │                         │
   │   {filename, size, mime}    │                         │
   │ ───────────────────────────►│                         │
   │                             │   issue presigned PUT   │
   │                             │   (server-side check:   │
   │                             │    quota, mime allow,   │
   │                             │    user can upload)     │
   │                             │                         │
   │   { id, putUrl, fields,     │                         │
   │     expiresAt }             │                         │
   │ ◄───────────────────────────│                         │
   │                             │                         │
   │   PUT putUrl                                          │
   │ ──────────────────────────────────────────────────────►
   │                                                       │
   │   200                                                 │
   │ ◄──────────────────────────────────────────────────────
   │                             │                         │
   │   POST /media/{id}/commit   │                         │
   │ ───────────────────────────►│                         │
   │                             │  HEAD s3:/{key}         │
   │                             │ ────────────────────────►
   │                             │  size, etag             │
   │                             │ ◄────────────────────────
   │                             │                         │
   │                             │  sniff first 4KB,       │
   │                             │  validate mime,         │
   │                             │  enqueue async jobs:    │
   │                             │   - virus scan          │
   │                             │   - probe (dims, exif)  │
   │                             │   - eager variants      │
   │                             │   - transcode (video)   │
   │                             │                         │
   │   { media object,           │                         │
   │     status: "processing" }  │                         │
   │ ◄───────────────────────────│                         │
```

Notes:

- The `media` row is created in `status='uploading'` at the `POST /media/uploads` step with an idempotency key. If the client never commits, a background job hard-deletes the row and S3 object after 24h.
- The presigned PUT carries restrictive policy: max size (re-asserted from the client claim with a small slack), single mime type, content-disposition forced to `attachment` for non-image types.
- After commit, the Go server fetches the **first 4KB only** via S3 ranged GET and runs mime sniffing (`net/http.DetectContentType` plus a richer library like `gabriel-vasile/mimetype`). If the sniffed mime contradicts the claim, the upload is rejected and the S3 object scheduled for deletion. This catches "renamed `.exe` to `.jpg`" and similar.
- We **do not** trust the file extension. The extension is recomputed from the sniffed mime for the canonical storage key.

### 3.2 Server-side upload (fallback)

For files under ~5MB and for environments where the client can't do direct-to-S3 (CSP issues, corporate proxies stripping presigned auth headers), the browser POSTs multipart to `/media/uploads/direct`. The Go server streams the body to S3 with chunked writes, runs the same validation, returns the media object. This is the lower-throughput path.

### 3.3 Multipart upload (large files, video)

For files >100MB (configurable; 5GB default cap), we use S3 multipart upload coordinated by the server:

```
Browser → POST /media/uploads/multipart {filename, size, mime}
          ← { id, uploadId, partCount, parts: [{partNumber, url}, ...] }

Browser PUTs each part to its presigned URL in parallel (default concurrency = 4).
On each completion, the client tracks the ETag.

Browser → POST /media/{id}/multipart/complete { parts: [{partNumber, etag}, ...] }
          ← { media object, status: "processing" }
```

Resumability falls out of this naturally: the client can retry any failed part, and pause/resume from local state. Abandoned multipart uploads are reaped by a daily job that calls `ListMultipartUploads` and aborts anything older than 48h.

### 3.4 Virus scanning hook

Mime sniffing and size limits stop the lazy attack; they don't stop a clean-looking image with a malicious payload (e.g., an HTML file served with `image/svg+xml` and JavaScript inside, a polyglot PDF/JPEG, a zip-bomb). We expose two hooks:

- **`media.scan` capability**: a plugin (or builtin ClamAV adapter) can register a scanner. The job runs after commit; on detection, the media row transitions to `status='quarantined'` and the URL is blocked. A scan that takes longer than 60s is killed and treated as "skip" with a logged warning.
- **Third-party API option**: built-in adapter for VirusTotal / Sublime / etc., configurable per install. Async, same status transition.

SVG is special: it's an XML document that can carry script. We strip on upload (using a deny-list sanitizer based on `bluemonday` configured for SVG: drop `<script>`, `on*` attributes, `<foreignObject>`). The sanitized version is what we store; the raw original goes to a `quarantine/` prefix for forensics, never served. Sites that need raw SVG (e.g., for an admin uploading their own logo) can opt in per-role via a capability `media:upload-raw-svg`.

### 3.5 Status state machine

```
uploading ─► processing ─► ready
                │ │
                │ └──► error  (recoverable; admin can retry)
                │
                └────► quarantined  (virus or sanitization failure)

ready ─► deleted-soft ─► deleted-hard   (cleanup job; see §16)
```

---

## 4. Media Data Model

The DB is the source of truth for metadata. The bucket is the source of truth for bytes. They are reconciled only at write time and via a daily audit job; ad-hoc reads do **not** stat S3.

### 4.1 `media` table

```sql
-- Fixed per review (contract S1): all PKs are UUID v7; FKs are UUID.
CREATE TABLE media (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    owner_id        UUID NOT NULL REFERENCES users(id),
    tenant_id       UUID,                        -- nullable for single-tenant
    status          TEXT NOT NULL                -- see §3.5
                       CHECK (status IN ('uploading','processing','ready',
                                         'error','quarantined',
                                         'deleted-soft','deleted-hard')),
    mime            TEXT NOT NULL,               -- sniffed, canonical
    size_bytes      BIGINT NOT NULL,
    storage_driver  TEXT NOT NULL,               -- 's3','r2','gcs','azure','fs'
    storage_bucket  TEXT NOT NULL,
    storage_key     TEXT NOT NULL,               -- e.g. orig/2026/05/abc123.jpg
    checksum_sha256 BYTEA,                       -- computed after upload
    filename        TEXT NOT NULL,               -- original user-supplied (sanitized)

    -- Image-specific
    width           INT,
    height          INT,
    has_alpha       BOOLEAN,
    dominant_color  TEXT,                        -- hex, for blur-up placeholders
    blurhash        TEXT,                        -- 20-30 bytes, for LQIP

    -- A/V-specific
    duration_ms     INT,
    has_audio       BOOLEAN,
    has_video       BOOLEAN,

    -- Editorial
    alt_text        TEXT,                        -- accessibility; see §10
    caption         TEXT,
    description     TEXT,
    title           TEXT,

    -- Extracted/derived
    metadata        JSONB NOT NULL DEFAULT '{}', -- EXIF, IPTC, codec info, etc.
    text_content    TEXT,                        -- extracted body text (pdf,
                                                 --   docx, etc.) for search
    collection_id   UUID REFERENCES collections(id) ON DELETE SET NULL,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at    TIMESTAMPTZ,                 -- when status moved off 'uploading'
    deleted_at      TIMESTAMPTZ,                 -- soft delete

    -- Convenience for orphan cleanup
    last_attached_at TIMESTAMPTZ,                -- updated when any reference appears
    reference_count  INT NOT NULL DEFAULT 0      -- denormalized; see §16
);

CREATE INDEX idx_media_owner_created ON media (owner_id, created_at DESC);
CREATE INDEX idx_media_status        ON media (status) WHERE status <> 'ready';
CREATE INDEX idx_media_mime_prefix   ON media (substring(mime FROM '^[^/]+'));
CREATE INDEX idx_media_collection    ON media (collection_id, created_at DESC);
CREATE INDEX idx_media_text_fts      ON media USING GIN (to_tsvector('simple',
                                       coalesce(filename,'') || ' ' ||
                                       coalesce(alt_text,'') || ' ' ||
                                       coalesce(title,'') || ' ' ||
                                       coalesce(text_content,'')));
```

Design notes:

- **`id` is a UUID v7.** URLs are `/img/{id}/...` (this is the same value that earlier drafts called `public_id`; the separate column has been removed in favor of a single UUID v7 PK per contract S1, which is already time-sortable and non-leaky).
- **`reference_count` is denormalized.** It's incremented/decremented by an after-write trigger on `media_refs` (see §4.3). A daily reconciliation job recomputes the truth to catch drift. We could remove this and always count on read, but the orphan-cleanup job runs over every media row and `WHERE reference_count = 0` is much cheaper than a correlated count.
- **`metadata` is JSONB**, not separate columns, because EXIF has hundreds of fields, many camera-specific, and we don't want a schema migration each time a phone vendor adds one.

### 4.2 `variants` table

A variant is a derived rendition. We never derive a variant from another variant — always from the original — so this is a flat one-to-many.

```sql
-- Fixed per review (contract S1): UUID v7 PK, UUID FK to media.
CREATE TABLE variants (
    id            UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    media_id      UUID NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    spec_hash     CHAR(16) NOT NULL,            -- hash of canonical spec
    spec          JSONB NOT NULL,               -- {w, h, fit, fmt, q, ...}
    kind          TEXT NOT NULL,                -- 'image','video-rendition',
                                                --   'poster','hls-segment-set'
    mime          TEXT NOT NULL,
    size_bytes    BIGINT NOT NULL,
    width         INT,
    height        INT,
    storage_key   TEXT NOT NULL,                -- e.g. var/{media_id}/{spec_hash}.webp
    duration_ms   INT,                          -- for video renditions
    bitrate_kbps  INT,                          -- for video renditions
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_hit_at   TIMESTAMPTZ,                  -- updated lazily for LRU eviction

    UNIQUE (media_id, spec_hash)
);

CREATE INDEX idx_variants_last_hit ON variants (last_hit_at NULLS FIRST);
```

The `spec_hash` is a 64-bit hash of the canonical spec JSON (with keys sorted and defaults filled). The hash function is **stable across releases** — we don't tweak it, because that would orphan all our cached variants. If we ever need to evolve, we bump a `version` in the spec.

### 4.3 `media_refs` table

To support orphan cleanup and cascade-aware deletion, we track who is using each media. References come from post content (blocks), from featured image relations, from theme settings, from plugin storage.

```sql
-- Fixed per review (contract S1): UUID FK to media.
CREATE TABLE media_refs (
    media_id     UUID NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    ref_type     TEXT NOT NULL,                 -- 'post-featured','post-block',
                                                --   'theme-setting','plugin'
    ref_id       TEXT NOT NULL,                 -- type-specific; opaque
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (media_id, ref_type, ref_id)
);

CREATE INDEX idx_media_refs_id ON media_refs (ref_id);
```

Block-level refs are extracted by walking the block tree on post save. Plugins that store media references go through a media-ref API; if they don't, they're on their own (their media is "orphaned" from our perspective).

### 4.4 `collections` table (folders)

```sql
-- Fixed per review (contract S1): UUID v7 PK, UUID FKs.
CREATE TABLE collections (
    id          UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    parent_id   UUID REFERENCES collections(id) ON DELETE CASCADE,
    owner_id    UUID NOT NULL REFERENCES users(id),
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    path        LTREE NOT NULL,                 -- materialized path
    item_count  INT NOT NULL DEFAULT 0,         -- denormalized
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (parent_id, slug)
);

CREATE INDEX idx_collections_path ON collections USING GIST (path);
```

WordPress doesn't have folders. Several plugins fix this (FileBird, FolderPress, Real Media Library). It's a usability gap big enough that we ship it builtin. Implementation uses `ltree` for cheap subtree queries ("show me everything under /clients/acme"). A media item is in **at most one** collection — we resist the "tag everywhere" pattern because users keep telling us they want folders.

Bulk move is a single SQL `UPDATE media SET collection_id = $1 WHERE id = ANY($2) AND owner_id = $3`.

---

## 5. Image Processing Pipeline

### 5.1 The `/img/{id}/{spec}` proxy

Image transforms are expressed in the URL, where `{id}` is the `media.id` UUID v7:

```
/img/01HX8K7M.../w_800,h_600,fit_cover,fmt_webp,q_80.webp
/img/01HX8K7M.../w_400.avif
/img/01HX8K7M.../w_1600,dpr_2.jpg
```

The path segment is a compact, ordered key=value list. We parse it, canonicalize (sort keys, apply defaults, clamp limits), hash, then check Postgres `variants` for a hit. The query is `SELECT storage_key, mime FROM variants WHERE media_id=$1 AND spec_hash=$2`.

- **Hit**: 302 (or proxy stream) from S3. We update `last_hit_at` lazily (batched, every N requests via a Redis counter flushed by a job — see §16).
- **Miss**: enter the **single-flight coalescer** (see §5.3a) so that N concurrent requests for the same `(media_id, spec_hash)` produce exactly one libvips render. The winner writes to S3, inserts the variant row, and streams the bytes; followers wait on the same future and stream from the same buffer. Future requests for the same spec hit the variant row directly.

The proxy is **idempotent**: even if single-flight is bypassed (different process, different node), two concurrent misses for the same spec both render; we use `INSERT ... ON CONFLICT DO NOTHING` on the variants row and the second one's S3 write is wasted but harmless. Pragmatically, the CDN coalesces this on the way in, so we rarely see concurrent misses.

#### Spec grammar

| Key | Meaning | Default | Limit |
|---|---|---|---|
| `w` | width in px | original | 4096 |
| `h` | height in px | original | 4096 |
| `fit` | `cover` \| `contain` \| `fill` \| `inside` \| `outside` | `inside` | — |
| `fmt` | `webp` \| `avif` \| `jpeg` \| `png` \| `auto` | `auto` | — |
| `q` | quality 1–100 | format-dependent | 1–100 |
| `dpr` | device pixel ratio multiplier | 1 | 3 |
| `bg` | background hex for `fit=contain` | `ffffff` | — |
| `bl` | blur radius (1–50) | 0 | 50 |
| `crop` | `x,y,w,h` for explicit crop | — | within original |
| `focus` | `x,y` 0–1 normalized focal point for `fit=cover` | center | — |

**Security**: the URL is signed (HMAC) **only** when the request specifies parameters not in the allow-list (e.g., `crop`). Common public transforms (width/height/format/quality) are unsigned because we want CDN cache keys to be stable across users. We clamp `w * h * dpr² <= 16M pixels` to prevent decompression-bomb-by-resize.

#### Format negotiation

If `fmt=auto`, we honor `Accept`. Order of preference: `avif`, `webp`, original. We `Vary: Accept` and the CDN keys on the normalized accept. For browsers without `Accept` header reflection in their cache (legacy concern, mostly moot), the renderer can write explicit `<picture>` sources.

### 5.2 libvips

We use **libvips** via `govips` (cgo) over Sharp/ImageMagick. Reasons:

- 4–8× faster than ImageMagick on resize.
- Streaming pipeline: peak memory is O(scanline) not O(image), so a 50MP JPEG resize doesn't OOM a 256MB container.
- AVIF, WebP, JPEG, PNG, GIF, TIFF, HEIC out of the box.
- Animated WebP/GIF support if we want it (we'll bound size).

Tradeoffs are real and documented in §17.

### 5.3 Eager vs lazy generation

On upload commit, we eagerly generate a small set: `thumb (256w)`, `medium (768w)`, `large (1536w)`, all in WebP. Reasons:

- These three cover the admin grid, the editor insert UI, and almost all editorial reuse.
- Eager generation hides the first-request latency of the most-shared sizes.
- Eager generation runs as a single libvips pipeline (one decode of the original, three outputs), so it's cheap.

Everything else — alternate aspect ratios, AVIF renditions, theme-specific sizes — is **lazy**. The first hit pays a one-time cost (typically 50–200ms), then it's CDN-cached effectively forever.

This is the line that kills "regenerate thumbnails": theme changes don't require re-running anything. The new theme generates the URLs it wants; the proxy serves them on miss. No background job. No nag.

### 5.3a Single-flight on variant misses (fixed per review — gap C2)

When N requests hit a missing variant simultaneously (e.g., a homepage feature goes viral and 200 viewers hit `/img/{id}/w_1920.avif` at once on a cold cache), only one should trigger a libvips render; the others should wait on the same future. We use a **per-node single-flight pool** keyed by `(media_id, spec_hash)`:

```go
// pseudo-code; the real impl uses golang.org/x/sync/singleflight.
res, err, _ := group.Do(
    fmt.Sprintf("%s/%s", mediaID, specHash),
    func() (any, error) {
        return renderAndPersistVariant(ctx, mediaID, spec)
    },
)
```

The leader runs the libvips pipeline, writes to S3, inserts the `variants` row, and returns the bytes; followers attached to the same key receive the leader's result without doing any work. The CDN further coalesces cross-node duplicates (most thundering-herd cases on a single hot URL are absorbed by the CDN edge before reaching origin). On the rare miss-where-followers-land-on-different-nodes case, the `INSERT ... ON CONFLICT DO NOTHING` on `variants` and the idempotent S3 PUT (same key, same bytes) make the duplicate work harmless. Per-node concurrency on the libvips workers is bounded to prevent saturation; an admission queue gates excess requests with a short `Retry-After` and a 503 — back-pressure rather than queue-without-bound, which is the standard failure shape for thundering herds.

### 5.4 LQIP / placeholders

On commit we compute:

- **`blurhash`** (~28 bytes): a tiny perceptual hash that decodes to a blurred preview. Stored on the media row. The renderer base64-encodes it as a CSS `background-image: url(data:...)` so the placeholder appears instantly with zero network.
- **`dominant_color`**: even cheaper, for cases where blurhash is overkill.

These are computed inline at commit (cheap; <30ms for a typical image) so the media is "displayable" immediately, even if variants haven't been generated yet.

### 5.5 Pipeline sketch (Go)

```go
type ImageRenderer struct {
    vips    *govips.VIPS
    store   ObjectStore       // see §13
    db      *pgxpool.Pool
    limiter *rate.Limiter     // CPU-bound work
}

type Spec struct {
    Width, Height int
    Fit           FitMode
    Format        Format
    Quality       int
    DPR           float64
    Bg            color.RGBA
    Blur          float64
    Crop          *Rect
    Focus         *Point
}

func (s Spec) Canonicalize(orig MediaProps, accept string) Spec { ... }
func (s Spec) Hash() string { /* 16 hex chars */ }

func (r *ImageRenderer) Serve(ctx context.Context, mediaID uuid.UUID,
    rawSpec string, accept string) (Response, error) {

    media, err := r.db.LoadMedia(ctx, mediaID)
    if err != nil { return nil, err }
    if media.Status != "ready" { return notReadyPlaceholder(media), nil }

    spec, err := parseSpec(rawSpec)
    if err != nil { return nil, errBadSpec }
    spec = spec.Canonicalize(media.Props(), accept)

    if v, ok := r.db.FindVariant(ctx, media.ID, spec.Hash()); ok {
        r.markHit(ctx, v.ID)
        return r.store.GetWithCacheHeaders(ctx, v.StorageKey, v.MIME)
    }

    // Miss path
    if err := r.limiter.Wait(ctx); err != nil { return nil, err }

    orig, err := r.store.GetReader(ctx, media.StorageKey)
    if err != nil { return nil, err }
    defer orig.Close()

    out, info, err := r.vips.Pipeline(orig, spec)
    if err != nil { return nil, err }

    key := variantKey(media.PublicID, spec.Hash(), info.MIME)
    if err := r.store.Put(ctx, key, out, info.MIME); err != nil { return nil, err }

    _ = r.db.InsertVariant(ctx, Variant{
        MediaID: media.ID, SpecHash: spec.Hash(), Spec: spec,
        Kind: "image", MIME: info.MIME, SizeBytes: info.Size,
        Width: info.W, Height: info.H, StorageKey: key,
    }) // ON CONFLICT DO NOTHING

    return Response{
        Body: bytes.NewReader(out), MIME: info.MIME,
        CacheControl: "public, max-age=31536000, immutable",
        ETag: spec.Hash(),
    }, nil
}
```

Three details worth dwelling on:

- The CPU limiter (`rate.Limiter` configured to ~`GOMAXPROCS` concurrent renders) prevents a flood of cache misses from pinning all cores and starving REST traffic.
- **`Cache-Control: public, max-age=31536000, immutable`**: because the spec hash is in the URL, the URL changes if any transform changes. So the response is immutable for that URL. This is the key to a 95%+ CDN hit ratio.
- We update `last_hit_at` *only on Postgres miss → S3 hit*, not on every CDN hit. We don't see the CDN hits; we don't need to.

---

## 6. Responsive Images

The renderer never emits a raw `<img src="...">` to user-visible content. It uses an `Image` helper that generates `srcset` and `sizes`. Two callers: Next.js's `<Image>` component and our own server-rendered helper for theme HTML.

### 6.1 Next.js `<Image>`

We configure `next.config.js` to use a custom loader pointing at our proxy:

```js
images: {
  loader: 'custom',
  loaderFile: './image-loader.js',
  deviceSizes: [360, 640, 768, 1024, 1280, 1536, 1920, 2560],
  imageSizes: [16, 32, 64, 96, 128, 192, 256, 384],
  formats: ['image/avif', 'image/webp'],
}
```

`image-loader.js` returns `https://cdn.example.com/img/{id}/w_{width},q_{q},fmt_auto`.

This is the path of least resistance for app-router pages and React Server Components. Blurhash gets passed as `placeholder="blur" blurDataURL="..."`.

### 6.2 Server-side helper (themes, blocks)

For server-rendered HTML (block render functions, classic themes), we ship a helper:

```go
// In the Go renderer
func RenderImage(m Media, opts ImageOpts) template.HTML
```

```ts
// Mirror in the JS renderer (for client blocks)
function image(m: Media, opts: ImageOpts): JSX.Element
```

It emits a `<picture>` for content negotiation when we want explicit format selection, otherwise an `<img srcset sizes>`:

```html
<picture>
  <source type="image/avif"
    srcset="/img/9e7f.../w_400,fmt_avif 400w,
            /img/9e7f.../w_800,fmt_avif 800w,
            /img/9e7f.../w_1600,fmt_avif 1600w"
    sizes="(min-width: 1024px) 800px, 100vw">
  <source type="image/webp"
    srcset="/img/9e7f.../w_400,fmt_webp 400w, ..."
    sizes="...">
  <img src="/img/9e7f.../w_800,fmt_jpeg"
       srcset="/img/9e7f.../w_400,fmt_jpeg 400w, ..."
       sizes="..."
       width="800" height="600"
       alt="..."
       loading="lazy"
       decoding="async"
       style="background-image:url(data:image/svg+xml;base64,...blurhash...);background-size:cover;">
</picture>
```

The `width` and `height` attributes match the *original* aspect, so the browser reserves layout space (CLS = 0). The blurhash is inlined as a tiny SVG-encoded gradient.

`loading="lazy"` is added unless the caller specifies `priority` (e.g., above-the-fold hero).

---

## 7. Video & Audio

### 7.1 Pipeline

On upload commit of a video, we enqueue a **transcode job**:

1. ffprobe to extract duration, codec, resolution, bitrate, audio tracks.
2. Generate **poster** at 10% offset (or user-specified second).
3. Generate **HLS ladder**: renditions at 1080p/720p/480p/360p, capped at the source resolution (no upscaling).
4. Each rendition is `H.264 + AAC` baseline-compatible for v1. (AV1 + Opus is a v2 conversation; encode times are higher and the player story is more nuanced.)
5. Output: a master `.m3u8`, per-rendition `.m3u8`s, and 6-second `.ts` segments. All in S3 under `var/{media_id}/hls/`.

Each output is a `variants` row of `kind='video-rendition'` or `'hls-segment-set'`. The master playlist gets `kind='hls-master'` and is what the player references.

We deliberately avoid DASH for v1. HLS works in every browser (Safari natively, others via hls.js), and shipping two manifests doubles storage cost.

### 7.2 Player

Frontend uses `<video controls>` with `hls.js` polyfill for non-Safari. Lazy-loaded. No third-party player SDK by default; a plugin can swap in Plyr or Video.js.

### 7.3 Audio

Same pipeline, simpler: transcode to MP3 + AAC for compatibility. No HLS. Generate a waveform PNG as a poster.

### 7.4 Where transcoding runs

In-process ffmpeg in the Asynq worker for v1. We start with a single queue named `media-transcode` with low concurrency (1–2 per worker, configurable). For installs that get large enough to need it, we'll factor a separate `transcode-worker` binary that scales independently. The interface (job payload, status updates) is designed to support this from day one.

---

## 8. Document Files

PDF, DOCX, PPTX, XLSX, ODT — common authoring outputs.

On commit:

1. **Thumbnail extraction**: first page rendered to PNG via `pdftoppm` (poppler) for PDF, LibreOffice headless for office docs. Generated as a variant of `kind='image'`. The admin treats this as the preview image.
2. **Text extraction**: full text → `media.text_content`. For PDF, `pdftotext`. For office docs, LibreOffice's `--convert-to txt`. For very large documents we truncate to ~1MB of text (search beyond that gets diminishing returns).

Both run as Asynq jobs. Failure is non-fatal: the media is `ready`, with a generic-document icon as the placeholder, and text search just doesn't match.

Office document conversion runs in a hardened sandbox (a separate container in production deployments). LibreOffice has had its share of CVEs and we're feeding it untrusted input.

---

## 9. Storage Drivers

Behind an interface:

```go
type ObjectStore interface {
    PresignPut(ctx context.Context, key string, opts PresignOpts) (URL, error)
    PresignGet(ctx context.Context, key string, ttl time.Duration) (URL, error)
    Put(ctx context.Context, key string, body io.Reader, mime string) error
    Get(ctx context.Context, key string) (io.ReadCloser, *ObjectInfo, error)
    Head(ctx context.Context, key string) (*ObjectInfo, error)
    Delete(ctx context.Context, key string) error
    InitMultipart(ctx context.Context, key string, parts int, mime string) (MultipartInit, error)
    CompleteMultipart(ctx context.Context, key string, uploadID string, parts []Part) error
    AbortMultipart(ctx context.Context, key string, uploadID string) error
    ListPrefix(ctx context.Context, prefix string, cursor string) (ObjectPage, error)
}
```

Implementations:

| Driver | Notes |
|---|---|
| `s3` | AWS S3 (the reference) |
| `r2` | Cloudflare R2, S3-compatible API, no egress fees |
| `gcs` | Google Cloud Storage |
| `azure` | Azure Blob |
| `fs` | Local filesystem, dev-only by default. Serves through the Go server. Refuses to start in production unless `--unsafe-fs-in-prod` is set. |
| `memory` | In-memory, for tests |

We **do not** support a generic FTP/SFTP driver. The API surface is wrong (no atomic multipart, weak head semantics).

R2 deserves a callout: it has no egress fees. For sites with heavy media, this is the difference between $50/mo and $5000/mo. Our default config for SaaS deployments points to R2.

---

## 10. Alt Text & Accessibility

`alt_text` is a column, not a metadata field. We elevate it for three reasons:

1. **Required-with-soft-enforcement**: the editor warns when an image block is saved without alt text, but doesn't block. The admin dashboard has a "Media missing alt text" report.
2. **Searchable**: the FTS index includes it.
3. **AI suggestion, opt-in**: an Asynq job can call a vision model to suggest alt text. **Off by default**, configurable per-install. The result is **suggested**, never auto-applied. The editor shows the suggestion as a chip the author can click to accept. This is a feature, not a shortcut around author accountability.

Decorative images: a checkbox in the editor sets `alt=""` explicitly, which is the correct accessible behavior and silences the nag.

Captions and titles render where authors put them (figure/figcaption); we do not auto-render `alt` as a visible caption.

---

## 11. Bulk Operations

The admin's media library is a virtualized grid (we expect users with 10k+ items). Bulk operations:

- **Bulk upload**: a drop zone that handles many files in parallel. Each file goes through the §3 flow. The UI tracks per-file progress with a rolling window of `media.status` polls (or websocket if available; polling for v1).
- **Bulk delete**: marks `deleted_at` and decrements references. Hard delete after 30 days (configurable). Bulk-deleting orphans skips the soft step on request.
- **Bulk move**: assigns `collection_id`. Single UPDATE.
- **Bulk re-tag** (alt-text suggest, regenerate from AI): an Asynq fan-out job iterating selected IDs.

The API:

```
POST /media/bulk
{
  "ids": ["uuid", ...],         // OR "filter": { ... }
  "op":  "delete" | "move" | "tag" | "ai-alt-suggest",
  "args": { ... }
}
→ 202 Accepted, { jobId, total }
```

Status polled at `/jobs/{id}`.

---

## 12. Cleanup, Orphans, Retention

Three background concerns:

### 12.1 Unattached / orphan media

`reference_count = 0` AND `committed_at < now() - interval '30 days'`:

- Move to `deleted-soft`, reclaim no storage yet.
- Surface in the admin's "Cleanup" view so users can recover.

`deleted-soft` AND `deleted_at < now() - interval '30 days'`:

- Hard delete: remove S3 object, cascade-delete variants (which also S3-delete their bytes), delete row.

Both thresholds are configurable per install. Saas defaults: 30/30. Self-host defaults: 60/60 (more conservative, since they may not be watching the dashboard daily).

### 12.2 Abandoned uploads

`status='uploading'` AND `created_at < now() - interval '24 hours'`: hard delete row and S3 object (and abort any open multipart). Daily job.

### 12.3 Cold variants

If a variant hasn't been hit in 180 days (`last_hit_at`), and it isn't one of the eager defaults (`thumb`/`medium`/`large`), drop the S3 object and the variant row. Future requests regenerate.

This is the win that makes lazy generation safe: we don't accumulate forever. The original is preserved; only derivatives are pruned.

---

## 13. CDN

Cloudflare in front by default (and free for the bulk of users at any reasonable scale). The Go server's image proxy is the **origin**.

Config:

- **Image variants** (`/img/...`): `Cache-Control: public, max-age=31536000, immutable`. Cache key includes path + normalized `Accept`. CDN holds these effectively forever.
- **Originals** (rarely served directly): same headers; URL includes hash so it's immutable.
- **HLS playlists** (`.m3u8`): `max-age=60` (some segments may shift if we re-encode). Segments themselves: `max-age=31536000, immutable`.
- **Public site HTML**: `Cache-Control: public, s-maxage=60, stale-while-revalidate=600`. ISR (§14.2) replaces this for most pages.
- **Admin / authenticated**: `Cache-Control: private, no-store`.

`Vary` is set narrowly: `Accept` for the image proxy when `fmt=auto` is in use, never `Cookie` (which would poison the cache for unauth users).

We **do not** rely on Cloudflare Image Resizing or Polish. Our proxy is fully portable: drop in any CDN (Fastly, Bunny, CloudFront) without losing transforms.

---

## 14. Migration Import (from WordPress)

The big WP import (covered in `08-migration-compat.md`) calls into the media subsystem for attachments. Specific to media:

- **Preserve URLs.** The importer records the original WP URL (`/wp-content/uploads/2023/03/photo.jpg`) on the media row's metadata. A redirect map (`old_url → /img/{media_id}/...`) is populated.
- **301 from old URLs.** A middleware checks the redirect map for any 404 in the `/wp-content/uploads/` prefix and emits 301 → the new variant URL. After 90 days the redirects stay (SEO permanence) but we stop logging them.
- **Sizes.** WP's per-theme size definitions are ignored. The new site's renderer asks for what it wants; the proxy serves. Old URLs in imported content for specific sizes (`photo-300x200.jpg`) redirect to the equivalent `/img/.../w_300,h_200,fit_cover.jpg`.
- **EXIF preservation.** Originals are stored exactly as uploaded.

---

## PART B — PERFORMANCE

## 15. Caching Layers

Five layers, ordered by proximity to the user:

```
   Client (browser cache, SW)
        │
        ▼
   CDN edge (Cloudflare)
        │
        ▼
   Next.js Data Cache + ISR Page Cache
        │
        ▼
   Go fragment / object cache (Redis)
        │
        ▼
   Postgres
```

### 15.1 HTTP cache (CDN + browser)

- `Cache-Control: public, s-maxage=..., stale-while-revalidate=...` on cacheable GETs.
- `ETag` set to a content hash; `If-None-Match` honored.
- `Last-Modified` set to `updated_at` for non-hash-keyed responses.
- `Vary` minimal (see §13).
- 304 responses are emitted from the Go origin for cache validation.

We rely on **stale-while-revalidate** for graceful degradation: when a backend goes slow, the CDN keeps serving the previous version while we refresh in the background.

### 15.2 Page cache via ISR

The public Next.js site uses **Incremental Static Regeneration**:

- Pages are statically generated on first request and cached at the Next.js layer + CDN.
- `revalidate` defaults: home/blog index = 60s, single post = 300s, taxonomy archives = 120s, marketing pages = 3600s.
- **On content change**, the Go backend POSTs to a Next.js webhook `/api/revalidate` with a list of affected paths/tags.

```
Editor saves a post
       │
       ▼
Go backend writes Postgres, invalidates Go caches (§16)
       │
       ▼
Go enqueues an Asynq job: "revalidate"
       │
       ▼
Job POSTs Next.js webhook:
  { tags: ["post:123", "term:5", "home"], paths: ["/blog/my-post"] }
       │
       ▼
Next.js calls revalidateTag() / revalidatePath()
       │
       ▼
Next.js purges Cloudflare via API for those paths (cache-tag header)
```

The Go-to-Next webhook is signed with a shared secret. It's the only way Next learns about backend writes.

### 15.3 Fragment cache (Go, Redis)

For expensive subqueries the renderer or API needs repeatedly:

- "Popular posts in the last 7 days"
- "Top 10 commenters this month"
- "Sidebar widget: recent comments"

```go
type FragmentCache interface {
    Get(ctx context.Context, key string) ([]byte, bool, error)
    Set(ctx context.Context, key string, val []byte, tags []string, ttl time.Duration) error
    InvalidateTags(ctx context.Context, tags ...string) error
}
```

Stored in Redis as `frag:{key}` plus a `tag:{tag}` set whose members are keys. `InvalidateTags` does a `SUNION` and `DEL` pipeline. We bound the size of each tag set (LRU-trim at 100k); if a tag is touching that many fragments, the cache hierarchy is wrong.

Default TTL: 5 minutes. Belt-and-braces; tag invalidation is the primary mechanism, TTL is a safety net for bugs where invalidation is forgotten.

### 15.4 Object cache (Go, Redis)

A short-TTL cache for hot DB reads. Used by:

- The post-by-slug lookup (`post:slug:{slug}` → `post.id`, 60s TTL).
- The "front page" post list (`post-list:home` → JSON, 30s TTL).
- Taxonomy term lookups.

Implemented as a generic wrapper:

```go
func Cached[T any](
    ctx context.Context, cache *redis.Client, key string,
    ttl time.Duration, tags []string,
    load func(context.Context) (T, error),
) (T, error)
```

We don't try to be cleverer than this. The single most damaging thing in WP perf is people stacking `wp_object_cache` plugins and persistent backends and getting stale state. We choose a small set of cached reads, write down what's cached, and tag them.

### 15.5 Block render cache

Each block's server render output is cached keyed by `(block_type, attrs_hash, content_version)`:

- `block_type` = e.g. `core/latest-posts`
- `attrs_hash` = stable hash of block attributes
- `content_version` = a monotonic counter bumped on any relevant content change for that block (e.g., for a `latest-posts` block, this is the global post version)

```
key = "block:" + block_type + ":" + attrs_hash + ":" + content_version
```

Bumping `content_version` is **the** invalidation mechanism. A new post → bump the global post version. Subsequent renders of `core/latest-posts` see a new key → cache miss → re-render. Old keys age out via TTL. This is functionally equivalent to a content-hash key but cheaper to compute (one Redis INCR vs hashing N posts).

Static blocks (paragraph, heading) have no `content_version` — their rendering depends only on attributes. They cache effectively forever.

This layer is opt-in per block type. The block declares it in its server-render contract: `cacheable: true | false | { ttl, version_tags: [...] }`.

---

## 16. Cache Invalidation Strategy

> "There are only two hard things in Computer Science: cache invalidation and naming things." — Phil Karlton

We choose **tag-based invalidation as the primary mechanism**, with short TTL as a safety net. Three rules:

1. Every cached entry has at least one tag.
2. Every mutation enumerates the tags it invalidates.
3. Tag invalidation is **atomic with the mutation** (transactional outbox; see below).

### 16.1 Tag naming (canonical — fixed per review, contract S5)

Tags are dotted lowercase. **This vocabulary is canonical**; docs 03 and 04 reference it rather than minting parallel names.

- `post:{uuid}` — a specific post
- `term:{uuid}` — a taxonomy term
- `user:{uuid}` — a user (for author pages, comments)
- `type:{slug}` — all content of a given post type (e.g., `type:event` for the event archive)
- `archive:{type}:{taxonomy}:{term-uuid}` — a taxonomy-scoped archive (e.g., `archive:post:category:01HX...`)
- `media:{uuid}` — a media item (for variants list)
- `nav:{uuid}` — a navigation menu (FK id, not display name)
- `site:settings` — site-wide settings / theme (the catch-all for theme changes; previously called `theme`)
- `global` — nuclear option

All `{uuid}` placeholders are UUID v7 strings (`gen_uuid_v7()` output). Earlier doc-04 examples like `query:posts:type=event` and doc-03 examples like `posttype:post` / `theme:active` / `site:*` are superseded by this list.

### 16.2 Transactional outbox

When a write happens, we don't invalidate caches mid-transaction (could roll back). Instead:

```
BEGIN;
  UPDATE posts SET ... WHERE id = $1;
  INSERT INTO cache_invalidations (tags, created_at)
    VALUES (ARRAY['post:123','term:5','post-list:home'], now());
COMMIT;
```

A dedicated worker (`invalidation-worker`) tails `cache_invalidations` and:

1. Calls `FragmentCache.InvalidateTags`.
2. Calls Next.js's `/api/revalidate` webhook.
3. Purges CDN cache-tags via Cloudflare API.
4. Deletes the row.

If the worker crashes, the row remains; on restart it picks up where it left off. If a single row's invalidation fails, it's retried with exponential backoff. We can replay the table for forensic "what got invalidated when."

#### Who can write to `cache_invalidations`

The outbox has three classes of first-class producers, all writing rows in the same shape:

1. **Core mutations.** Post/term/user/menu/settings writes append a row inside their own transaction.
2. **The renderer.** When `content_rendered` is regenerated and pre-render-cache entries become stale, the renderer enqueues invalidations against the affected block-render keys.
3. **Plugins** (canonical contract S6 — fixed per review). Plugins invalidate cache via a host ABI call

       host.cache.invalidate(tags: []string)

   gated by the `cache.invalidate` capability. The wire format and capability gate live in **doc 02 §6 (Plugin DB & host capabilities)**; this doc references that ABI surface rather than redefining it. The host implementation translates the call into a `cache_invalidations` row written inside the plugin's current transaction (so plugin mutations and their cache effects roll back together). Plugins **cannot** write to the outbox by direct SQL — they don't have `db.write` on core tables — so the ABI is the only path. This is essential for B4 (plugin mutations that change rendered output, e.g. an SEO plugin updating meta tags) so that ISR doesn't go silently stale.

### 16.3 What's tagged with what

| Cache entry | Tags |
|---|---|
| `/blog/[slug]` page | `post:{id}`, `term:{each}`, `theme` |
| `/category/[slug]` page | `term:{id}`, `term-tree:category`, `theme` |
| `core/latest-posts` block render | `post-list:global`, `theme` |
| Sidebar widget "recent comments" | `comment-list:global` |
| Image variant | `media:{id}` |

Posts updates invalidate `post:{id}` + `post-list:global` + each term tag. Term updates invalidate `term:{id}` + `term-tree:{taxonomy}` + (heavy) all pages tagged with that term. Theme changes invalidate `theme` (which is a global purge, intentionally — theme changes are rare).

---

## 17. Database Performance

### 17.1 Connection pooling

`pgx` with `pgxpool`. Defaults:

- `MaxConns = 8 * NumCPU`, but no less than 16, no more than 80.
- `MinConns = max(2, MaxConns/8)` to keep some warm.
- `MaxConnLifetime = 30m`; `MaxConnIdleTime = 5m`.

For most installs, that's overkill. The defaults are tuned for not-noticing-the-knob.

### 17.2 Read replicas (later)

The schema and access layer reserve a `ReadDB *pgxpool.Pool` separate from `WriteDB`. v1 wires both to the primary. v2 lets operators point them at a replica. Reads are routed through `ReadDB` *except*:

- Read-your-write contexts: when a request just performed a write, the next read in the same request uses `WriteDB`.
- Auth/session reads: always `WriteDB` (replica lag risks are bad here).

### 17.3 N+1 prevention

GraphQL resolvers use **dataloader** (per-request batching + caching). For the post → author → comments shape:

```go
loaders := dataloader.New(req,
    dataloader.WithBatch(userLoader, 100),
    dataloader.WithBatch(commentLoader, 100),
)
```

REST endpoints don't get free dataloader batching, so we write explicit `IN (...)` queries for batch fetches. Code review rejects N+1.

We periodically run integration tests with `pg_stat_statements` enabled and a "query count budget" per endpoint (e.g., `/blog/[slug]` ≤ 4 queries). CI fails on budget breaches.

### 17.4 Indexes

Each table doc (in `01-core-cms.md` and this file) declares indexes inline. The convention:

- Foreign-key columns: indexed.
- `created_at DESC` listings: composite index `(owner_id, created_at DESC)` (or whatever the partition is).
- Partial indexes for hot subsets (e.g., `WHERE status <> 'ready'`).
- GIN for JSONB only when we actually query into it (we resist the "GIN everything" pattern — they're expensive to maintain).

### 17.5 Slow query log

`log_min_duration_statement = 200ms` in production. A daily job aggregates the log into a "top 20 slow queries" report visible in the admin's perf panel. Operators can pin queries from the report for `EXPLAIN ANALYZE`; the panel runs it on a read replica (or primary with `STATEMENT_TIMEOUT`).

`pg_stat_statements` enabled, surfaced in the same panel.

---

## 18. Asset Delivery

### 18.1 JS/CSS bundling

App code (admin, public site, editor): Next.js's bundler (currently Turbopack/webpack). Code-splitting per route by default.

Theme assets: we recommend themes ship their code as ES modules that Next.js imports. For themes that need to live outside Next (e.g., classic themes with a separate build), they declare an entrypoint in `theme.json`; we wire a Vite build into the deploy pipeline. **Recommendation: Next-managed.** Simpler, gets the perf benefits for free.

### 18.2 Code splitting

- Per-route: Next.js default.
- Per-block (editor): each block is dynamically imported in the editor (`React.lazy`); only the blocks present in a page's content load on the public site.
- Per-feature (admin): the media library page doesn't pull in the editor; the editor doesn't pull in user management.

### 18.3 Preloading

The renderer emits:

- `<link rel="preload" as="image" fetchpriority="high">` for the LCP image (typically the post's featured image or first content image).
- `<link rel="preload" as="font" crossorigin>` for the theme's primary fonts (declared in `theme.json`).
- `<link rel="modulepreload">` for the next-route bundles when a `next/link` is in-viewport.

### 18.4 Early Hints (103)

Where the CDN supports it, we issue **103 Early Hints** with the same `Link: ...; rel=preload` headers we'd emit in the document. Cloudflare and Fastly do; some others don't. Behind a feature flag in v1 because we want to gauge real-world impact before defaulting it on.

We do **not** use HTTP/2 Server Push. It's effectively deprecated and removed from Chrome.

---

## 19. Edge Rendering

Next.js supports two server runtimes: Node and Edge (V8 isolates). Edge has faster cold starts and is geographically distributed, but no Node APIs (no `pg`, no `fs`).

Our split:

| Route | Runtime | Reason |
|---|---|---|
| `/` (home) | **Edge + ISR** | Static after first build; CDN-cacheable. |
| `/blog/[slug]` | **Edge + ISR** | Same as above. Falls back to Node on rare on-demand revalidation, fetched via REST. |
| `/blog` (index), taxonomy archives | **Edge + ISR** | Same. |
| `/admin/*` | **Node** | Authenticated, server-rendered, talks to Postgres. |
| `/api/*` (Next API routes) | mixed | Auth check on Edge where possible, data fetch on Node. |
| Anything personalized (logged-in headers) | **Node** | Edge can't talk to Redis/Postgres in v1. |

The pattern: **Edge fetches via REST/GraphQL over HTTPS**; Node has direct DB access. Edge routes call into the Go API (same network if co-located, anywhere if Cloudflare Workers).

For Vercel-hosted installs this is native. For self-hosted, "Edge" maps to a standard Node deployment colocated with the Go API; we don't run user code in isolates.

---

## 20. Real-User Monitoring (RUM)

A small inlined script (~2KB minified, no dependencies) collects:

- **Core Web Vitals**: LCP, INP, CLS, TTFB, FCP.
- **Page metadata**: route template, theme version, plugins active (anonymized).
- **Device**: device pixel ratio, viewport, effective connection type, country (derived server-side from IP).

It sends a single `navigator.sendBeacon('/_rum', payload)` on `visibilitychange:hidden` (the only event that reliably fires when the user leaves).

Storage: a `web_vitals` partitioned table (by week) in Postgres. We don't try to be Datadog — but we have enough to answer "is my LCP regressing?" without paying for a SaaS.

Admin dashboard ships a "Performance" tab with:

- P50/P75/P95 of each vital over the last 7/30 days.
- Per-route breakdown.
- Regression alerts (P75 worse than last week's by ≥20%).

Plugins can register additional collectors via a documented hook.

Opt-out: a `Do-Not-Track` header and a config flag. By default we collect from authenticated admin users only for self-host installs; SaaS opts in to anonymous collection (covered in privacy policy).

---

## 21. Synthetic Benchmarks

`gonext bench` is a CLI command shipping with the Go binary:

```
$ gonext bench --url https://example.com
Running benchmark against https://example.com ...

TTFB (cold)    180 ms
TTFB (warm)     42 ms
LCP (warm)     780 ms
INP (estimated) 90 ms
JS transferred  142 KB (gzipped)
Image bytes     310 KB
Total bytes     489 KB
Page weight grade: A

Compared to baseline (saved 2 weeks ago):
  TTFB warm: 42ms (+3ms) ━━━
  LCP warm: 780ms (-25ms) ▓▓▓
```

Implementation: a headless Chrome over CDP (we ship `chromedp`); the `--no-chrome` flag skips Chrome-driven measurements and reports server-side timings only. Useful in CI and for users who want to compare hosts ("I moved from $5 shared to $20 VPS, did it help?").

Output is human-readable by default, `--json` for machines. Stores a `bench_runs` history in Postgres so the comparison ("vs last week") is automatic.

---

## 22. Performance Budget

Themes and plugins declare their assets in their manifest. CI tooling (`gonext bench --budget`) enforces:

| Asset class | Default budget | Hard cap |
|---|---|---|
| Theme JS (gzipped, per route) | 100 KB | 200 KB |
| Theme CSS (gzipped, per route) | 40 KB | 80 KB |
| Plugin JS (per plugin, gzipped) | 30 KB | 60 KB |
| Plugin CSS (per plugin) | 10 KB | 20 KB |

Budgets are warnings by default; hard caps are errors that block a theme from being installed (overridable by site admins who Really Mean It with a config flag).

The marketplace (later) rejects themes that exceed budgets at submission time.

This is the single biggest lesson from WP: there is no quality gate. A theme can ship 800KB of jQuery slider plugins and the user has no idea. We won't accept that.

---

## 23. Optimistic & Streaming Patterns

### 23.1 Link prefetch on hover

The default `<Link>` component prefetches the route on hover with a 100ms delay (to avoid prefetching every link the cursor crosses). On mobile, prefetch on `touchstart`.

### 23.2 Optimistic updates in admin

For high-frequency interactions:

- Marking a comment spam: optimistic UI update, rollback on error.
- Reordering blocks: optimistic local state, save-on-debounce.
- Toggling a published/draft status: optimistic with confirmation toast.

We don't use optimistic for destructive actions (delete) — confirmation dialog first.

### 23.3 Streaming SSR + Suspense

Next.js's App Router supports streaming. We use it where the page has:

- A fast above-the-fold (post body, hero) → renders immediately.
- A slower below-the-fold (related posts, comments) → wrapped in `<Suspense>`, streamed after.

The first byte ships in <100ms; the rest follows. LCP gets dramatically better on slow connections.

---

## 24. Trade-offs & Rejected Alternatives

### 24.1 libvips vs Sharp vs ImageMagick

- **libvips (chosen)**: fastest, lowest memory, mature in production at scale (Cloudinary, Discourse).
- **Sharp**: it's libvips with a Node wrapper. We're in Go; we use the underlying libvips directly (`govips`).
- **ImageMagick / GraphicsMagick**: more familiar to admins, more permissive of weird inputs. Much slower, higher memory ceiling, more historical CVEs. Rejected as the primary; could be a fallback for exotic formats.

Cost: cgo dependency, harder to build static binaries. We ship a Dockerfile and a build script that handles the libvips link; for self-hosters, the published binary is glibc-linked Linux for the common case.

### 24.2 Our own proxy vs imgix / Cloudinary / Cloudflare Image Resizing

- **Imgix/Cloudinary**: $$. Vendor lock-in (URL schemes are not portable). Great UX, but our users include people moving off WordPress *because* it's gotten expensive.
- **Cloudflare Images / Image Resizing**: cheap, fast, but locks you into Cloudflare. Doesn't help if a user wants a different CDN.
- **Our proxy (chosen)**: full control, portable across CDNs, no per-image cost. The libvips bet is on long-term efficiency: at 1M images/month, our amortized cost is fractions of a cent per image vs $0.001+ at SaaS providers.

### 24.3 Page cache vs ISR

- **Traditional page cache** (Varnish / WP Super Cache style): a full HTML cache in front of the app, invalidated on writes. Works great until cache invalidation gets messy (logged-in users, ESI for fragments, etc.).
- **ISR (chosen)**: the framework knows what's cacheable, revalidation is integrated with the build system, and per-page TTL is declarative. The trade-off is that ISR keys aren't as granular as Varnish ESI; we compensate with the Go fragment cache for the granular case.

We considered both. ISR is the right primary; we keep Cloudflare's HTML cache (effectively the CDN layer) as a second tier with low TTL.

### 24.4 GraphQL dataloader vs SQL views

For complex listings (a homepage with posts + featured images + comment counts + author + categories), an alternative is a Postgres view that joins it all. We use both:

- A `post_card_view` view denormalizes the common card-rendering shape, refreshed on post write.
- Dataloader for ad-hoc shapes the GraphQL client requests.

The view gives single-query reads for the hot path; dataloader covers the long tail.

### 24.5 HLS vs DASH vs progressive MP4

- **Progressive MP4**: simple, works everywhere, but no adaptive bitrate. Bad on mobile / variable connections.
- **DASH**: open standard, broad support except Safari (which needs hls.js fallback or native HLS).
- **HLS (chosen)**: native Safari, hls.js elsewhere. One manifest format, simpler ops.

### 24.6 Eager all sizes vs eager-three + lazy rest

- **Eager all sizes** (WP's approach for declared sizes): upload is slow, switching themes means regeneration plugin.
- **Lazy all sizes**: first visitor to any page eats the rendering latency.
- **Eager-three + lazy rest (chosen)**: covers the common case fast, lazy covers the long tail without the regeneration headache.

### 24.7 Per-tenant vs shared bucket

For SaaS deployments, do tenants share an S3 bucket or get their own?

- **Shared bucket** (chosen for v1): cheaper, simpler IAM, single CDN config. Tenant isolation is logical (key prefix) and DB-enforced.
- **Per-tenant bucket**: stronger isolation, easier "export everything for tenant X" + "delete everything for tenant X" stories. More bucket quota management. Worth revisiting at v2 for enterprise tier.

### 24.8 RUM in-house vs SaaS

- **SaaS** (Sentry, Datadog, Vercel Analytics): great products, real cost per pageview, lock-in.
- **In-house (chosen)**: a tiny script + a Postgres table + a dashboard tab is enough for 95% of users. SaaS RUM can be a plugin for the rest.

---

## 25. Security touch-points

Cross-references to the auth/permissions doc. Specific to media/perf:

- **Presigned URLs are scoped** by mime, max size, key prefix. Expire in ≤15min.
- **SVG sanitization** on upload (§3.4).
- **Decompression bombs**: image proxy clamps output pixels (§5.1). On the upload path, we read EXIF without rendering and reject if declared dimensions exceed 16M pixels.
- **EXIF stripping (optional)**: a config flag strips EXIF on variants. Originals retain EXIF; we don't lie about what the user uploaded.
- **Hot-link protection (optional)**: a config flag enforces `Referer` checks on image variants from the public domain. Off by default; some users have linked sites that depend on hotlinking.
- **DoS via cache-miss flood**: an attacker hitting many never-rendered specs would force libvips work. The image proxy enforces a leaky-bucket per-IP (e.g., 5 cache misses/sec) and a global concurrency cap. CDN absorbs the rest.
- **Server-side request forgery**: the proxy only reads from our own S3; it never fetches arbitrary URLs from clients. We have no remote-fetch feature.

---

## 26. Operational Notes

- **Backups**: Postgres backed up with PITR; S3 has versioning + lifecycle. Media is part of the backup story (not "easier said than done": the backup tooling explicitly enumerates the buckets and pins lifecycle rules).
- **Restore drill**: a `gonext restore` command can rehydrate a site from a Postgres backup + S3 bucket within an hour. Tested quarterly.
- **Migration of bucket** (e.g., S3 → R2): an Asynq job copies objects via streaming get/put, then flips `storage_driver` per row. Reversible.
- **Tail latencies**: the perf panel surfaces `p95` and `p99`, not just `p50`. The slow tail is where users feel WordPress is slow even when the average is fine.

---

## 27. Open Questions

1. **AI alt-text vendor lock-in.** Do we ship a built-in default (e.g., OpenAI vision), or pure-bring-your-own-key? Built-in is friendlier; bring-your-own is more honest about cost.
2. **Animated AVIF vs animated WebP vs APNG.** Worth supporting? GIF uploads should transcode to something, but to what? Lean toward WebP-animated for broad support.
3. **Edge runtime for the image proxy.** Cloudflare Workers can't run libvips. Can we run a subset (resize, format-convert) in WASM-vips at the edge for ultra-low-latency first-render? Promising; complicated. Park for v2.
4. **Per-tenant CDN config.** SaaS customers may want their own CDN account (for vanity domains, custom WAF). Multi-CDN per install is doable; not in v1.
5. **DASH support for video.** Only if real users ask. Most won't.
6. **Bandwidth metering / quota.** When (not if) do we add per-tenant bandwidth caps? Likely needed for SaaS economics; design hooks exist.
7. **Cache-tag header support on origin CDNs other than Cloudflare.** Fastly has `Surrogate-Key`. Bunny is weaker. Spec a "CDN driver" interface like we did for storage?
8. **Should the proxy support remote URLs** (`/img/proxy?url=...`)? Convenient for migrations and embeds, but SSRF risk and weird caching semantics. Lean: no in v1, gated plugin in v2.
9. **Background blur for `fit=cover` smart-crop.** A saliency model would pick the focus point automatically. Useful, expensive, model dependency. Worth investigating with a lightweight model.
10. **Per-request CPU accounting for the image proxy.** We have a global limiter; we don't bill per render. For SaaS, this is a future need (a hostile customer could grind cores).

---

## 28. Summary table — what this doc decides

| Decision | Choice |
|---|---|
| Upload default path | Direct-to-S3 presigned PUT; server fallback for small files. |
| Image library | libvips via `govips`. |
| Variant generation | Eager: thumb/medium/large in WebP. Lazy: everything else, cached forever (in URL) + LRU pruned (in storage). |
| Image URL shape | `/img/{media_id}/{spec}.{ext}`, immutable, CDN-cached. |
| Format negotiation | `Accept`-based, `Vary: Accept`. |
| Video | HLS via ffmpeg in Asynq. H.264+AAC v1. |
| Documents | Thumbnail via poppler/LO. Text extracted to `media.text_content`. |
| Folders | Native, single-collection-per-item, ltree. |
| Storage drivers | S3 / R2 / GCS / Azure / fs (dev). |
| CDN | Cloudflare default; portable across CDNs (no resizing-at-edge dependency). |
| Page cache | ISR via Next.js + Cloudflare for HTML; tag-based revalidation. |
| Fragment cache | Redis, tag-keyed, TTL safety net. |
| Object cache | Generic `Cached[T]` wrapper for hot reads. |
| Block render cache | Per-block opt-in, `(type, attrs_hash, content_version)` key. |
| Invalidation | Transactional outbox → invalidation-worker → Redis + Next.js + CDN. |
| DB | pgx pool; read replicas reserved for v2; dataloader for GraphQL. |
| RUM | In-house, ~2KB beacon, Postgres-stored, admin dashboard. |
| Synthetic | `gonext bench` CLI with baseline comparison. |
| Perf budget | Manifest-declared, CI-enforced, marketplace-gated. |
| Edge runtime | Home / posts / archives on Edge + ISR; admin/personalized on Node. |

---

*End of doc. Cross-doc dependencies: media model touches `01-core-cms.md` (post→featured-image relation), `04-block-editor.md` (image block render), `08-migration-compat.md` (WP attachment importer + URL redirect map). Perf budget touches `03-theme-system.md` (theme manifest) and `02-plugin-system.md` (plugin manifest).*
