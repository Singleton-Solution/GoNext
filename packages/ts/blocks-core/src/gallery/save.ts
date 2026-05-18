/**
 * `core/gallery` save serializer + server-render hint.
 *
 * Galleries persist as a flat list of image descriptors plus column count
 * and crop strategy. We render the WP-compatible `<figure
 * class="wp-block-gallery">` wrapper with one `<figure
 * class="wp-block-image">` per image — this matches what the legacy WP
 * importer emits, so existing content lands on this block without a
 * rewrite step.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** A single image in a gallery. */
export interface GalleryImage {
  /** Image URL. */
  url: string;
  /** Alt text — empty string allowed for decorative images. */
  alt: string;
  /** Optional caption rendered inside `<figcaption>`. */
  caption?: string;
  /** Intrinsic pixel width (used for the `width` attribute + CLS hint). */
  width?: number;
  /** Intrinsic pixel height. */
  height?: number;
}

/** Attribute shape for `core/gallery`. */
export interface GalleryAttributes extends BlockAttributes {
  /** The images in the gallery, in display order. */
  images: GalleryImage[];
  /** Visible column count (1..8). Defaults to 3 in the editor. */
  columns?: number;
  /** When true, thumbnails are cropped to a uniform aspect ratio. */
  imageCrop?: boolean;
}

function galleryClasses(attrs: GalleryAttributes): string[] {
  const cols = attrs.columns ?? 3;
  return [
    'wp-block-gallery',
    `columns-${cols}`,
    attrs.imageCrop !== false ? 'is-cropped' : null,
  ].filter((c): c is string => c !== null);
}

/** Render a single child `<figure class="wp-block-image">`. */
function renderImage(img: GalleryImage): string {
  const width = img.width !== undefined ? ` width="${img.width}"` : '';
  const height = img.height !== undefined ? ` height="${img.height}"` : '';
  const tag = `<img src="${escapeHtml(img.url)}" alt="${escapeHtml(img.alt)}"${width}${height}/>`;
  const caption = img.caption
    ? `<figcaption>${escapeHtml(img.caption)}</figcaption>`
    : '';
  return `<figure class="wp-block-image">${tag}${caption}</figure>`;
}

/**
 * Pure serializer. We always emit the wrapper figure so theme CSS targeting
 * `.wp-block-gallery` applies even when the gallery is empty.
 */
export function save({
  attributes,
}: BlockSaveProps<GalleryAttributes>): string {
  const children = attributes.images.map(renderImage).join('');
  return `<figure${classAttr(galleryClasses(attributes))}>${children}</figure>`;
}

export function serverRender(
  attrs: GalleryAttributes,
  _innerHtml: string,
): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
