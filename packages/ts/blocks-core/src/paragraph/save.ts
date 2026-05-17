/**
 * `core/paragraph` save serializer + server-render hint.
 *
 * The save output is the canonical HTML representation persisted in the
 * block tree's pre-rendered content shadow. It mirrors what the Go render
 * walker emits, so the editor's "what you wrote" preview lines up with
 * the live page byte-for-byte for plain text.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/paragraph`. */
export interface ParagraphAttributes extends BlockAttributes {
  /** The paragraph text. Plain string for now; rich inline runs land later. */
  content: string;
  /** Inserter-applied text alignment. Optional — defaults to start-aligned. */
  align?: 'left' | 'center' | 'right';
  /** Theme drop-cap toggle. Renders the first letter in an oversized run. */
  dropCap?: boolean;
}

/**
 * Build the className list for the rendered `<p>`. We keep the BEM-ish
 * `gn-block-paragraph` prefix so theme CSS can target core output without
 * fighting plugin styles.
 */
function paragraphClasses(attrs: ParagraphAttributes): string[] {
  return [
    'gn-block-paragraph',
    attrs.align ? `has-text-align-${attrs.align}` : null,
    attrs.dropCap ? 'has-drop-cap' : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Pure serializer — used by the editor's save pipeline. Same shape as the
 * server-render hint so a round-trip through validate → save lands on
 * bytes the Go walker would have produced.
 */
export function save({ attributes }: BlockSaveProps<ParagraphAttributes>): string {
  return `<p${classAttr(paragraphClasses(attributes))}>${escapeHtml(attributes.content)}</p>`;
}

/**
 * Server-render hint. Paragraph is a leaf block, so `_innerHtml` is always
 * empty and we ignore it explicitly — naming it `_innerHtml` keeps the
 * signature parallel to the container blocks without tripping the
 * `noUnusedParameters` TS rule.
 */
export function serverRender(attrs: ParagraphAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
