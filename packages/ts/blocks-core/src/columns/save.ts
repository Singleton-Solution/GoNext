/**
 * `core/columns` save serializer + server-render hint.
 *
 * Container block. The Go render walker hands the already-rendered inner
 * HTML in as the second argument; the save() form re-emits the wrapper but
 * leaves a sentinel comment where children would go — the editor's full
 * Save pipeline (which knows the whole tree) replaces the sentinel.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr } from '../internal/escape.ts';

/** Attribute shape for `core/columns`. */
export interface ColumnsAttributes extends BlockAttributes {
  /** Number of visual columns (2..6). The schema enforces the bound. */
  columns: number;
  /** Whether to stack on small viewports. */
  isStackedOnMobile?: boolean;
  /** Vertical alignment of the column track. */
  verticalAlignment?: 'top' | 'center' | 'bottom';
}

const INNER_SENTINEL = '<!--gn-inner-blocks-->';

function columnsClasses(attrs: ColumnsAttributes): string[] {
  return [
    'gn-block-columns',
    `gn-block-columns--cols-${attrs.columns}`,
    attrs.isStackedOnMobile !== false ? 'is-stacked-on-mobile' : null,
    attrs.verticalAlignment
      ? `is-vertically-aligned-${attrs.verticalAlignment}`
      : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Save output. Inner children are deferred to the walker via the sentinel.
 * `serverRender` substitutes the rendered children in.
 */
export function save({ attributes }: BlockSaveProps<ColumnsAttributes>): string {
  return `<div${classAttr(columnsClasses(attributes))}>${INNER_SENTINEL}</div>`;
}

export function serverRender(attrs: ColumnsAttributes, innerHtml: string): string {
  return `<div${classAttr(columnsClasses(attrs))}>${innerHtml}</div>`;
}

/** Exposed for the walker / tests that need to substitute manually. */
export const COLUMNS_INNER_SENTINEL = INNER_SENTINEL;
