/**
 * `core/image` save serializer + server-render hint.
 *
 * Images render as `<figure><img/></figure>` to keep alt text + caption
 * grouped semantically. The intrinsic dimensions are emitted as width/height
 * attributes so the browser can reserve layout space (CLS-friendly) without
 * a CSS round-trip.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/image`. */
export interface ImageAttributes extends BlockAttributes {
  /** Image URL. We accept any string here; the editor enforces URL shape. */
  url: string;
  /** Required alt text. Empty string is allowed for decorative images. */
  alt: string;
  /** Optional caption rendered inside `<figcaption>`. */
  caption?: string;
  /** Intrinsic pixel width (used for the `width` attribute + CLS hint). */
  width?: number;
  /** Intrinsic pixel height. */
  height?: number;
  /** Layout-level alignment exposed to themes. */
  align?: 'left' | 'center' | 'right' | 'wide' | 'full';
  /** Optional link wrapping the image. */
  href?: string;
}

function figureClasses(attrs: ImageAttributes): string[] {
  return [
    'gn-block-image',
    attrs.align ? `align${attrs.align}` : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Pure serializer. We always emit the figure wrapper so theme CSS can target
 * `.gn-block-image figcaption` uniformly even when no caption is present.
 */
export function save({ attributes }: BlockSaveProps<ImageAttributes>): string {
  const width = attributes.width !== undefined ? ` width="${attributes.width}"` : '';
  const height = attributes.height !== undefined ? ` height="${attributes.height}"` : '';
  const img = `<img src="${escapeHtml(attributes.url)}" alt="${escapeHtml(attributes.alt)}"${width}${height}/>`;
  const linked = attributes.href
    ? `<a href="${escapeHtml(attributes.href)}">${img}</a>`
    : img;
  const caption = attributes.caption
    ? `<figcaption>${escapeHtml(attributes.caption)}</figcaption>`
    : '';
  return `<figure${classAttr(figureClasses(attributes))}>${linked}${caption}</figure>`;
}

export function serverRender(attrs: ImageAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
