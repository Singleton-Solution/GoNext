/**
 * Marketplace admin — shared types.
 *
 * Mirrors the wire shapes emitted by the API's
 * `/api/v1/admin/marketplace/*` handlers
 * (apps/api/internal/admin/marketplace/model.go). We hand-maintain the
 * projection rather than generate it from the OpenAPI surface
 * (#240 still pending); when the codegen lands this file flips to a
 * re-export of the generated types.
 *
 * Optional fields mirror the server's `omitempty` behaviour: every
 * value the handler omits when zero is typed as undefined here so the
 * UI never crashes on a sparse response.
 */

/** A single rating row, as returned by GET /listings/{slug}/ratings. */
export interface RatingRow {
  user_id: string;
  stars: number;
  review_text?: string;
  created_at: string;
}

/** Aggregate of stars across one version's ratings. */
export interface RatingAggregate {
  average: number;
  count: number;
}

/** Response body for GET /listings/{slug}/ratings. */
export interface RatingsResponse {
  aggregate: RatingAggregate;
  ratings: RatingRow[];
}

/** Compatibility range — one row of plugin_compat_matrix. */
export interface CompatRow {
  host_min: string;
  host_max: string;
  tested: boolean;
}

/**
 * One version row, exactly as the handler emits it. The wasm digest
 * is exposed as hex; the bytes themselves stay in object storage.
 * The optional `compat` field is populated by the
 * GET /listings/{slug}/versions endpoint (the detail endpoint
 * includes only the latest version without its matrix).
 */
export interface VersionRow {
  id: string;
  version: string;
  wasm_sha256_hex: string;
  signature_hex?: string;
  published_at: string;
  deprecated: boolean;
  deprecated_at?: string;
  manifest: string;
  compat?: CompatRow[];
}

/** Compact projection used by the grid. */
export interface ListingCard {
  id: string;
  slug: string;
  name: string;
  summary: string;
  homepage_url?: string;
  license_spdx?: string;
  primary_category?: string;
  stars: number;
  rating_count: number;
  install_count: number;
  created_at: string;
}

/** Full detail returned by GET /listings/{slug}. */
export interface ListingDetail extends ListingCard {
  author_id?: string;
  status: string;
  updated_at: string;
  latest_version?: VersionRow;
}

/** Sort modes the catalogue supports. Must mirror the backend enum. */
export type SortKey = 'recent' | 'stars' | 'popular';

/** Install endpoint response. */
export interface InstallResponse {
  slug: string;
  version: string;
  plugin_slug: string;
}

/** Action-style result, matching the plugins admin module's contract. */
export type ActionResult<T = void> =
  | { ok: true; data?: T }
  | { ok: false; error: string };
