/**
 * `core/spacer` save serializer + server-render hint.
 *
 * Empty vertical space, sized via inline `height`. The actual height number
 * is bounded by the schema to keep authors from typing the page off-screen.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/spacer`. */
export interface SpacerAttributes extends BlockAttributes {
  /** Height in pixels (1–2000). */
  height: number;
}

export function save({ attributes }: BlockSaveProps<SpacerAttributes>): string {
  const style = `height:${attributes.height}px`;
  return `<div class="gn-block-spacer" aria-hidden="true" style="${escapeHtml(style)}"></div>`;
}

export function serverRender(attrs: SpacerAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
