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
 * chip selected"; "image"/"video"/"document" map to the server-side
 * mime-class predicate. The server treats any other value as a 400
 * — we keep this union narrow on the client so a typo can't sneak
 * through.
 */
export type MediaTypeFilter = 'all' | 'image' | 'video' | 'document';

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
