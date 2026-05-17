/**
 * `core/list` save serializer + server-render hint.
 *
 * Lists model an `<ul>` or `<ol>` with a flat array of item strings. Nested
 * lists are deferred — they require a recursive item shape that we'll add
 * once the rich-text inline model lands. The current scalar-array shape is
 * what the WP-style "convert paragraph to list" command produces, and it's
 * what every renderer downstream expects today.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/list`. */
export interface ListAttributes extends BlockAttributes {
  /** Bullet (`ul`) or numbered (`ol`). */
  ordered: boolean;
  /** Flat list of item strings. */
  values: string[];
  /** For ordered lists: the starting integer. Defaults to 1. */
  start?: number;
  /** For ordered lists: when true, reverse the numbering. */
  reversed?: boolean;
}

function listClasses(attrs: ListAttributes): string[] {
  return [
    'gn-block-list',
    attrs.ordered ? 'gn-block-list--ordered' : 'gn-block-list--unordered',
  ];
}

/**
 * Pure serializer. Renders each item inside an `<li>`. Item strings are
 * HTML-escaped; the rich-inline phase will replace this with a tree of
 * formatting marks.
 */
export function save({ attributes }: BlockSaveProps<ListAttributes>): string {
  const tag = attributes.ordered ? 'ol' : 'ul';
  const start =
    attributes.ordered && attributes.start !== undefined
      ? ` start="${attributes.start}"`
      : '';
  const reversed = attributes.ordered && attributes.reversed ? ' reversed' : '';
  const items = attributes.values
    .map((v) => `<li>${escapeHtml(v)}</li>`)
    .join('');
  return `<${tag}${start}${reversed}${classAttr(listClasses(attributes))}>${items}</${tag}>`;
}

export function serverRender(attrs: ListAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
