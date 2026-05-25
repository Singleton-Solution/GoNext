/**
 * `core/query` save serializer + server-render hint.
 *
 * The Query block is the most "dynamic" of the core set: its persisted
 * shape is a *query specification* (filters + ordering + page size), not
 * the rendered list itself. Inner blocks define the **post-card template**
 * — at render time the Go walker fetches the matching posts and repeats
 * the inner-blocks tree once per post, substituting the rendered child
 * HTML into a single sentinel slot inside an `<ul class="...">` wrapper.
 *
 * The TS save() produces a *static-preview* shell. It walks none of the
 * data — that's the server's job — but it does emit the wrapper, attach
 * the query spec as `data-*` attributes (useful for plugin code that
 * wants to introspect a tree without re-parsing JSON), and reserve a
 * single inner-blocks sentinel where the per-post markup goes. The Go
 * render walker iterates the matching posts and substitutes a repeated
 * block of `<li>` items into that one sentinel.
 *
 * Mirrors core/columns and core/media-text for the sentinel handling, so
 * the same walker machinery applies uniformly.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Sort direction. Mirrors SQL ORDER BY clause direction. */
export type QueryOrder = 'asc' | 'desc';

/**
 * Field to sort by. The server resolves these to schema columns at query
 * time; we expose a closed enum so plugin code can't slip arbitrary
 * column names through.
 */
export type QueryOrderBy = 'date' | 'title' | 'menu_order' | 'modified';

/** Attribute shape for `core/query`. */
export interface QueryAttributes extends BlockAttributes {
  /**
   * Maximum number of posts to render in one pass. Defaults to 10. The
   * schema bounds this at 1..50 so a malformed import can't accidentally
   * load thousands of rows.
   */
  limit?: number;
  /** Optional page offset (0-indexed). Combine with `limit` for paging. */
  offset?: number;
  /** Filter by author ID. */
  authorId?: string;
  /** Filter by category slug or ID. */
  category?: string;
  /** Filter by tag slug or ID. */
  tag?: string;
  /** Search term — server resolves to the configured search backend. */
  search?: string;
  /** Sort direction. Defaults to `'desc'`. */
  order?: QueryOrder;
  /** Sort field. Defaults to `'date'`. */
  orderBy?: QueryOrderBy;
  /**
   * Whether to include sticky posts at the top of the list, regardless
   * of the active ordering. Mirrors WP behaviour.
   */
  sticky?: boolean;
  /**
   * Optional wrapper tag override. Defaults to `<ul>` — themes that
   * prefer a `<div>`-based list can pass `'div'`.
   */
  tagName?: 'ul' | 'div';
}

const INNER_SENTINEL = '<!--gn-query-loop-->';

/** Default query attributes — applied at render time when fields are unset. */
export const QUERY_DEFAULTS = {
  limit: 10,
  offset: 0,
  order: 'desc' as QueryOrder,
  orderBy: 'date' as QueryOrderBy,
  tagName: 'ul' as 'ul' | 'div',
} as const;

function queryClasses(attrs: QueryAttributes): string[] {
  const orderBy = attrs.orderBy ?? QUERY_DEFAULTS.orderBy;
  const order = attrs.order ?? QUERY_DEFAULTS.order;
  return [
    'wp-block-query',
    'gn-block-query',
    `is-order-by-${orderBy}`,
    `is-order-${order}`,
  ];
}

/**
 * Build the `data-gn-query-*` attribute fragment. Each set attribute lands
 * as its own `data-*` so server-side filter plugins can read individual
 * fields without parsing a JSON blob.
 */
function dataAttrs(attrs: QueryAttributes): string {
  const limit = attrs.limit ?? QUERY_DEFAULTS.limit;
  const offset = attrs.offset ?? QUERY_DEFAULTS.offset;
  const order = attrs.order ?? QUERY_DEFAULTS.order;
  const orderBy = attrs.orderBy ?? QUERY_DEFAULTS.orderBy;

  const parts: string[] = [
    ` data-gn-query-limit="${limit}"`,
    ` data-gn-query-offset="${offset}"`,
    ` data-gn-query-order="${order}"`,
    ` data-gn-query-order-by="${orderBy}"`,
  ];
  if (attrs.authorId) {
    parts.push(` data-gn-query-author="${escapeHtml(attrs.authorId)}"`);
  }
  if (attrs.category) {
    parts.push(` data-gn-query-category="${escapeHtml(attrs.category)}"`);
  }
  if (attrs.tag) {
    parts.push(` data-gn-query-tag="${escapeHtml(attrs.tag)}"`);
  }
  if (attrs.search) {
    parts.push(` data-gn-query-search="${escapeHtml(attrs.search)}"`);
  }
  if (attrs.sticky) {
    parts.push(' data-gn-query-sticky="true"');
  }
  return parts.join('');
}

/**
 * Pure serializer. Static preview only — no posts are fetched here. The
 * single inner-blocks sentinel marks where the Go walker will splice the
 * per-post rendered fragments in.
 */
export function save({
  attributes,
}: BlockSaveProps<QueryAttributes>): string {
  const tag = attributes.tagName ?? QUERY_DEFAULTS.tagName;
  return `<${tag}${classAttr(queryClasses(attributes))}${dataAttrs(attributes)}>${INNER_SENTINEL}</${tag}>`;
}

/**
 * Server-render hint.
 *
 * The Go walker hands the already-rendered loop body in as `innerHtml`
 * — that body is the inner-blocks tree repeated once per matched post,
 * each repetition wrapped in an `<li>` (or the tag set by `tagName`).
 * We splice it into the sentinel slot and emit the final wrapper.
 *
 * Walker contract: when the query yields zero posts, `innerHtml` is an
 * empty string. The wrapper still renders so theme CSS targeting
 * `.wp-block-query` has a target for an empty-state message.
 */
export function serverRender(
  attrs: QueryAttributes,
  innerHtml: string,
): string {
  const tag = attrs.tagName ?? QUERY_DEFAULTS.tagName;
  return `<${tag}${classAttr(queryClasses(attrs))}${dataAttrs(attrs)}>${innerHtml}</${tag}>`;
}

/** Exposed for the walker / tests that need to substitute manually. */
export const QUERY_INNER_SENTINEL = INNER_SENTINEL;
