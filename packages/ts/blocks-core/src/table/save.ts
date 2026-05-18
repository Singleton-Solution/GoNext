/**
 * `core/table` save serializer + server-render hint.
 *
 * Tables persist as three matrices of strings — `head`, `body`, `foot` — so
 * the on-disk shape mirrors the visual rendering one-to-one. We keep the
 * cell payload as plain strings here; the inline-formatting layer will
 * widen this to a rich-text rope alongside the rest of the leaf blocks.
 *
 * The wrapper is a single `<table>` so theme CSS can target it with one
 * selector, and the optional `<caption>` is emitted first per the HTML
 * spec so screen readers announce it before the table contents.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** A single row is just a flat array of cell strings. */
export type TableRow = string[];

/** Attribute shape for `core/table`. */
export interface TableAttributes extends BlockAttributes {
  /** Optional `<thead>` rows. Empty array means no header. */
  head?: TableRow[];
  /** `<tbody>` rows — the main content. */
  body: TableRow[];
  /** Optional `<tfoot>` rows. Empty array means no footer. */
  foot?: TableRow[];
  /** Optional caption rendered inside `<caption>`. */
  caption?: string;
  /** Style modifiers. */
  style?: {
    /** Alternating-row striping. */
    stripes?: boolean;
    /** Visible cell borders. */
    borders?: boolean;
  };
}

function tableClasses(attrs: TableAttributes): string[] {
  return [
    'gn-block-table',
    attrs.style?.stripes ? 'is-style-stripes' : null,
    attrs.style?.borders ? 'is-style-borders' : null,
  ].filter((c): c is string => c !== null);
}

/** Emit a single row's `<td>` (or `<th>`) cells. */
function renderRow(row: TableRow, cellTag: 'td' | 'th'): string {
  const cells = row
    .map((cell) => `<${cellTag}>${escapeHtml(cell)}</${cellTag}>`)
    .join('');
  return `<tr>${cells}</tr>`;
}

/** Emit a `<thead>` / `<tbody>` / `<tfoot>` section, or nothing for empty input. */
function renderSection(
  sectionTag: 'thead' | 'tbody' | 'tfoot',
  rows: TableRow[] | undefined,
  cellTag: 'td' | 'th',
): string {
  if (!rows || rows.length === 0) {
    return '';
  }
  const inner = rows.map((row) => renderRow(row, cellTag)).join('');
  return `<${sectionTag}>${inner}</${sectionTag}>`;
}

/**
 * Pure serializer. Sections appear in DOM order: caption → thead → tbody →
 * tfoot. Empty sections are omitted entirely so the output stays compact.
 */
export function save({ attributes }: BlockSaveProps<TableAttributes>): string {
  const caption = attributes.caption
    ? `<caption>${escapeHtml(attributes.caption)}</caption>`
    : '';
  const head = renderSection('thead', attributes.head, 'th');
  const body = renderSection('tbody', attributes.body, 'td');
  const foot = renderSection('tfoot', attributes.foot, 'td');
  return `<table${classAttr(tableClasses(attributes))}>${caption}${head}${body}${foot}</table>`;
}

export function serverRender(attrs: TableAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
