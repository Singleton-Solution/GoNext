/**
 * `core/quote` save serializer + server-render hint.
 *
 * Pull quotes render as `<blockquote>` with an optional `<cite>` for the
 * attribution. The body is a single string; rich runs land alongside the
 * inline model upgrade.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/quote`. */
export interface QuoteAttributes extends BlockAttributes {
  /** The quoted text. */
  value: string;
  /** Optional citation, rendered inside `<cite>`. */
  citation?: string;
  /** Visual style toggle the theme can hook into. */
  style?: 'plain' | 'large';
}

function quoteClasses(attrs: QuoteAttributes): string[] {
  return [
    'gn-block-quote',
    attrs.style ? `is-style-${attrs.style}` : null,
  ].filter((c): c is string => c !== null);
}

export function save({ attributes }: BlockSaveProps<QuoteAttributes>): string {
  const citation = attributes.citation
    ? `<cite>${escapeHtml(attributes.citation)}</cite>`
    : '';
  return `<blockquote${classAttr(quoteClasses(attributes))}><p>${escapeHtml(attributes.value)}</p>${citation}</blockquote>`;
}

export function serverRender(attrs: QuoteAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
