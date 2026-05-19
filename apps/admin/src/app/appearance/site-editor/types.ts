/**
 * Shared types for the Site Editor Lite admin surface.
 *
 * The shapes mirror the JSON the API returns from
 * /api/v1/admin/site_editor — see apps/api/internal/admin/site_editor
 * for the canonical definitions on the Go side.
 *
 * We deliberately keep the block representation loose (`Block`'s
 * `attrs` is `Record<string, unknown>`) because the editor surface is
 * a tree of opaque-to-the-shell editor nodes — the renderer is the
 * concern of a downstream React component, not the page chrome.
 */

/**
 * A single block node. Mirrors the on-wire shape produced by the Go
 * `html2blocks.Convert` converter:
 *
 *   { name: "core/paragraph", attrs: { content: "..." }, innerBlocks?: [...] }
 *
 * NOTE the field names: `name` (not `type`) and `attrs` (not
 * `attributes`). These match the `html2blocks.Block` JSON tags so the
 * round-trip "parse on the server, save back to the server" never
 * goes through a schema translation step.
 */
export interface SiteEditorBlock {
  /** Registry name, e.g. "core/paragraph". */
  name: string;
  /** Loose attribute bag; narrowed by each block's registered schema. */
  attrs?: Record<string, unknown>;
  /** Nested children for container blocks (group, columns, …). */
  innerBlocks?: SiteEditorBlock[];
  /** Optional stable id; the converter leaves this empty. */
  id?: string;
}

/** The persisted block-tree document is just an array of root blocks. */
export type SiteEditorBlockTree = SiteEditorBlock[];

/**
 * One template part as returned by GET /parts. The shape is flat —
 * metadata + the resolved tree + a flag telling the UI whether the
 * tree came from an override.
 */
export interface SiteEditorPart {
  name: string;
  title: string;
  area: string;
  blocks: SiteEditorBlockTree;
  overridden: boolean;
}

/** The wrapping list response. */
export interface SiteEditorPartsResponse {
  theme: string;
  parts: SiteEditorPart[];
}

/** Body shape for PUT /parts/{name}. */
export interface SiteEditorPutPayload {
  blocks: SiteEditorBlockTree;
}

/** Response shape from PUT /parts/{name}. */
export interface SiteEditorPutResponse {
  theme: string;
  name: string;
  blocks: SiteEditorBlockTree;
  overridden: boolean;
}
