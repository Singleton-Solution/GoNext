/**
 * Media admin — shared types.
 *
 * Mirrors the JSON envelope returned by `GET /api/v1/admin/media`
 * (and its sibling routes). Keeping the on-wire shape one-for-one with
 * the Go-side `Asset`/`Page` types means the client layer doesn't
 * need a mapping step — we `as Asset` after the parse and call it a
 * day. When the Go contract changes (e.g. a new field), this file
 * changes with it.
 *
 * Nullability follows the Go side: optional fields use `?` and map
 * to `omitempty`-tagged fields on the Go struct.
 */

/**
 * A single media asset (image, video, document) as returned by the
 * list and detail endpoints.
 *
 * `public_url` is computed server-side from the storage config; the
 * client treats it as opaque. `width`/`height` are NULL for non-image
 * media (audio, PDF, etc.) and the UI hides the dimensions row when
 * they are absent.
 */
export interface MediaAsset {
  id: string;
  filename: string;
  mime_type: string;
  byte_size: number;
  width?: number;
  height?: number;
  alt_text: string;
  caption: string;
  storage_key: string;
  public_url?: string;
  uploader_id: string;
  created_at: string;
  updated_at: string;
  /** Folder the asset is filed in; omitted for root. Issue #69. */
  collection_id?: string;
  /** Lowercase, deduplicated tag list. Never null; defaults to []. Issue #71. */
  tags: string[];
}

/**
 * A media folder, mirrors `collections.Collection` on the Go side.
 * `path` is the dotted ltree path (e.g. "marketing.2026.q1") — the
 * admin tree reconstructs the hierarchy by splitting on the dot.
 */
export interface MediaCollection {
  id: string;
  slug: string;
  name: string;
  path: string;
  parent_id?: string;
  created_at: string;
  updated_at: string;
}

/** Envelope returned by `GET /admin/media/collections`. */
export interface CollectionListResponse {
  data: MediaCollection[];
}

/**
 * Body shape for `POST /admin/media/move`. `collection_id` omitted /
 * null moves the assets to the implicit root.
 */
export interface MoveMediaBody {
  ids: string[];
  collection_id?: string | null;
}

/**
 * Body shape for `POST /admin/media/bulk`. `params` is op-specific.
 * - delete: no params.
 * - move:   `{ collection_id?: string | null }`.
 * - tag:    `{ add?, remove?, set? }` — strings normalised server-side.
 * - ai-alt: no params.
 */
export interface BulkRequest {
  op: 'delete' | 'move' | 'tag' | 'ai-alt';
  ids: string[];
  params?: Record<string, unknown>;
}

/** Response shape for `POST /admin/media/bulk`. */
export interface BulkResult {
  op: string;
  succeeded: number;
  failed?: Record<string, string>;

  /**
   * HLS playlist URL for video assets — populated by the
   * media.video.transcode worker (#52). The video player picks HLS
   * over the raw mp4 source when this is set.
   */
  hls_url?: string;

  /**
   * True when the media_text row exists for this asset. The detail
   * page surfaces a "View extracted text" link based on this flag.
   * Issue #60.
   */
  has_extracted_text?: boolean;

  /**
   * True for assets registered in proxy mode by the migration
   * importer (#187). The grid shows a "proxied" badge so operators
   * can tell at a glance which assets live remotely.
   */
  is_proxied?: boolean;

  /**
   * Origin URL for proxied assets. Empty for locally-stored assets.
   */
  source_url?: string;
}

/**
 * The paginated list response — matches `media.Page` on the Go side.
 * `next_cursor` is empty when there is no more data.
 */
export interface MediaListResponse {
  data: MediaAsset[];
  pagination: {
    next_cursor: string;
  };
}

/**
 * Body shape for `PATCH /api/v1/admin/media/{id}`. Both fields are
 * optional; the server requires at least one to be present.
 *
 * Filename and storage_key are deliberately NOT part of this shape:
 * the server rejects attempts to mutate them via PATCH (they are
 * immutable for the row's lifetime; a rename surfaces as a re-upload).
 */
export interface MediaUpdateBody {
  alt_text?: string;
  caption?: string;
}

/**
 * The chip-filter classes the grid exposes. "all" means "no filter
 * chip selected"; "image"/"video"/"document"/"audio" map to the
 * server-side mime-class predicate. The server treats any other value
 * as a 400 — we keep this union narrow on the client so a typo can't
 * sneak through.
 */
export type MediaTypeFilter = 'all' | 'image' | 'video' | 'document' | 'audio';

/**
 * Local UI state for an upload-in-progress row. The dropzone holds
 * a list of these and renders progress bars + error messages from
 * them. Not a wire type — never round-trips through the server.
 */
export interface UploadProgress {
  id: string;
  filename: string;
  size: number;
  status: 'queued' | 'uploading' | 'done' | 'error';
  /** 0..1 — XHR progress fraction when known, else undefined. */
  progress?: number;
  /** Server's response on success — used to thread the new row into the grid. */
  asset?: MediaAsset;
  /** Human-readable error message when status === "error". */
  error?: string;
}
