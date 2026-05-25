/**
 * `core/query` Edit component.
 *
 * The Edit surface is a *placeholder* — we don't fetch real posts in
 * the editor (that would couple the editor canvas to the REST surface
 * and slow every keystroke). Instead we render a compact summary chip
 * showing the current query spec (limit, order, filters) above the
 * inner-blocks slot the canvas walker fills with the post-card
 * template. Authors see the template they're editing; the spec chip
 * tells them how many copies it'll repeat on the published page.
 *
 * Filter / order / limit knobs live in the Inspector panel — this
 * component only reflects whatever the current attributes say.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import { QUERY_DEFAULTS, type QueryAttributes } from './save.ts';

/**
 * Build a compact human-readable summary of the active query spec for
 * the editor placeholder chip. Surfaces the fields most likely to
 * affect the rendered count first (limit + filters), with the order
 * direction as a trailing hint.
 *
 * Exported so the inspector panel and tests can reuse the same format.
 */
export function summariseQuery(attrs: QueryAttributes): string {
  const limit = attrs.limit ?? QUERY_DEFAULTS.limit;
  const filters: string[] = [];
  if (attrs.authorId) filters.push(`author: ${attrs.authorId}`);
  if (attrs.category) filters.push(`category: ${attrs.category}`);
  if (attrs.tag) filters.push(`tag: ${attrs.tag}`);
  if (attrs.search) filters.push(`search: "${attrs.search}"`);

  const orderBy = attrs.orderBy ?? QUERY_DEFAULTS.orderBy;
  const order = attrs.order ?? QUERY_DEFAULTS.order;
  const tail = `${orderBy} ${order}`;

  if (filters.length === 0) {
    return `Up to ${limit} posts · ${tail}`;
  }
  return `Up to ${limit} posts · ${filters.join(', ')} · ${tail}`;
}

export function QueryEdit({
  attributes,
  isSelected,
}: BlockEditProps<QueryAttributes>): ReactElement {
  const className = [
    'wp-block-query',
    'gn-block-query',
    `is-order-by-${attributes.orderBy ?? QUERY_DEFAULTS.orderBy}`,
    `is-order-${attributes.order ?? QUERY_DEFAULTS.order}`,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  const summary = summariseQuery(attributes);

  // Inner-blocks slot — the canvas walker fills this with the post-card
  // template authors are editing. The chip above gives them a sense of
  // the query that will repeat that template at runtime.
  return (
    <div className={className} data-block="core/query">
      <div className="gn-block-query__placeholder" aria-live="polite">
        <strong className="gn-block-query__placeholder-title">Query Loop</strong>
        <span className="gn-block-query__placeholder-summary">{summary}</span>
      </div>
      <div
        className="gn-block-query__template"
        data-gn-inner-blocks="template"
      />
    </div>
  );
}

export default QueryEdit;
