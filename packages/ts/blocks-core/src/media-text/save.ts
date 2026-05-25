/**
 * `core/media-text` save serializer + server-render hint.
 *
 * Two-column layout pairing an image with a stack of inner-block content.
 * The image lives in its own column; the inner-blocks slot fills the
 * other. Authors flip the image between the start and end of the row via
 * `mediaPosition`, which is RTL-aware — the rendered class names map
 * `'left'` → `is-media-on-the-left` and `'right'` → `is-media-on-the-right`
 * exactly like WordPress's own Media & Text block, so theme CSS targeting
 * either side keeps working across locales.
 *
 * Container behaviour matches `core/columns`: the static `save()` emits a
 * sentinel where children would go, and the walker substitutes already-
 * rendered inner HTML via `serverRender(attrs, innerHtml)`.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/media-text`. */
export interface MediaTextAttributes extends BlockAttributes {
  /** Source URL for the media side. Empty string renders an empty figure. */
  mediaUrl: string;
  /** Required alt text. Empty string is allowed for decorative media. */
  mediaAlt: string;
  /** Image side of the row. Defaults to `left` (RTL flips this at the theme). */
  mediaPosition?: 'left' | 'right';
  /** Width of the media column as a percent (10..90). Defaults to 50. */
  mediaWidth?: number;
  /** Vertical alignment of the two columns relative to each other. */
  verticalAlignment?: 'top' | 'center' | 'bottom';
  /**
   * When true, the image fills its column edge-to-edge (no inner padding,
   * `object-fit: cover`). When omitted/false, the image keeps its
   * intrinsic aspect ratio inside the column.
   */
  imageFill?: boolean;
  /** Optional caption rendered inside the media `<figure>`. */
  mediaCaption?: string;
}

const INNER_SENTINEL = '<!--gn-inner-blocks-->';

/** Normalise `mediaWidth` into a 10..90 integer with 50 as the default. */
export function normalizeMediaWidth(raw: number | undefined): number {
  if (raw === undefined || Number.isNaN(raw)) return 50;
  const rounded = Math.round(raw);
  if (rounded < 10) return 10;
  if (rounded > 90) return 90;
  return rounded;
}

function mediaTextClasses(attrs: MediaTextAttributes): string[] {
  const pos = attrs.mediaPosition === 'right' ? 'right' : 'left';
  return [
    'gn-block-media-text',
    `is-media-on-the-${pos}`,
    attrs.verticalAlignment
      ? `is-vertically-aligned-${attrs.verticalAlignment}`
      : null,
    attrs.imageFill ? 'has-media-on-the-fill' : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Render the media `<figure>`. The figure is always emitted (even with an
 * empty URL) so themes targeting `.gn-block-media-text__media` get a
 * consistent grid cell — the editor uses the empty figure as the
 * placeholder slot.
 */
function renderMediaFigure(attrs: MediaTextAttributes): string {
  const inner = attrs.mediaUrl
    ? `<img src="${escapeHtml(attrs.mediaUrl)}" alt="${escapeHtml(attrs.mediaAlt)}"/>`
    : '';
  const caption = attrs.mediaCaption
    ? `<figcaption>${escapeHtml(attrs.mediaCaption)}</figcaption>`
    : '';
  return `<figure class="gn-block-media-text__media">${inner}${caption}</figure>`;
}

/**
 * Build the inline `grid-template-columns` style. We hand the browser the
 * exact split so themes don't have to re-derive it from a class — and so
 * the static save() can be rendered without a stylesheet (Go server-side
 * preview, RSS exports, syndicated copies).
 */
function gridStyle(
  attrs: MediaTextAttributes,
): string {
  const w = normalizeMediaWidth(attrs.mediaWidth);
  const left = attrs.mediaPosition === 'right' ? 100 - w : w;
  return `grid-template-columns:${left}% ${100 - left}%`;
}

/**
 * Pure serializer. Children of the inner-blocks (text) column are deferred
 * to the walker via `INNER_SENTINEL`; `serverRender` substitutes them in.
 */
export function save({
  attributes,
}: BlockSaveProps<MediaTextAttributes>): string {
  const media = renderMediaFigure(attributes);
  const content =
    `<div class="gn-block-media-text__content">${INNER_SENTINEL}</div>`;
  const ordered =
    attributes.mediaPosition === 'right' ? content + media : media + content;
  return `<div${classAttr(mediaTextClasses(attributes))} style="${gridStyle(attributes)}">${ordered}</div>`;
}

export function serverRender(
  attrs: MediaTextAttributes,
  innerHtml: string,
): string {
  const media = renderMediaFigure(attrs);
  const content =
    `<div class="gn-block-media-text__content">${innerHtml}</div>`;
  const ordered =
    attrs.mediaPosition === 'right' ? content + media : media + content;
  return `<div${classAttr(mediaTextClasses(attrs))} style="${gridStyle(attrs)}">${ordered}</div>`;
}

/** Exposed for the walker / tests that need to substitute manually. */
export const MEDIA_TEXT_INNER_SENTINEL = INNER_SENTINEL;
