/**
 * `core/heading` save serializer + server-render hint.
 *
 * Heading is a leaf block. The rendered tag (`h1`–`h6`) is driven by the
 * `level` attribute. We treat level 2 as the default — h1 is reserved for
 * the post title in most themes, and the inserter primes new headings to
 * the second level.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/heading`. */
export interface HeadingAttributes extends BlockAttributes {
  /** The heading text. */
  content: string;
  /** Heading rank: integer 1..6. */
  level: 1 | 2 | 3 | 4 | 5 | 6;
  /** Optional anchor for in-page links. The save output emits it as `id`. */
  anchor?: string;
  /** Text alignment. */
  align?: 'left' | 'center' | 'right';
}

function headingClasses(attrs: HeadingAttributes): string[] {
  return [
    'gn-block-heading',
    `gn-block-heading--level-${attrs.level}`,
    attrs.align ? `has-text-align-${attrs.align}` : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Pure serializer. Same input → same bytes.
 */
export function save({ attributes }: BlockSaveProps<HeadingAttributes>): string {
  const tag = `h${attributes.level}`;
  const id = attributes.anchor
    ? ` id="${escapeHtml(attributes.anchor)}"`
    : '';
  return `<${tag}${id}${classAttr(headingClasses(attributes))}>${escapeHtml(attributes.content)}</${tag}>`;
}

export function serverRender(attrs: HeadingAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
