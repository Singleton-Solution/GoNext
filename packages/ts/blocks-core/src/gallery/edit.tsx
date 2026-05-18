/**
 * `core/gallery` Edit component.
 *
 * The authoring surface is a thumbnail grid with simple "move left" /
 * "move right" reorder controls. We deliberately keep the controls
 * keyboard-first — drag-and-drop reordering arrives once the canvas DnD
 * primitive lands. The grid itself uses the same `columns-N` class as the
 * saved markup so what authors see matches what gets rendered.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { GalleryAttributes, GalleryImage } from './save.ts';

/** Immutable swap of two array entries. Returns a new array. */
function swap<T>(items: T[], a: number, b: number): T[] {
  if (a < 0 || b < 0 || a >= items.length || b >= items.length) {
    return items;
  }
  const next = [...items];
  const tmp = next[a] as T;
  next[a] = next[b] as T;
  next[b] = tmp;
  return next;
}

export function GalleryEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<GalleryAttributes>): ReactElement {
  const images: GalleryImage[] = attributes.images ?? [];
  const cols = attributes.columns ?? 3;
  const className = [
    'wp-block-gallery',
    `columns-${cols}`,
    attributes.imageCrop !== false ? 'is-cropped' : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  // Reorder commands. Wired only when the block is selected so an inert
  // preview can't be mutated accidentally by stray click handlers.
  const move = (idx: number, delta: number): void => {
    setAttributes({ images: swap(images, idx, idx + delta) });
  };

  if (images.length === 0) {
    return (
      <figure className={className} data-block="core/gallery">
        <span className="gn-block-gallery__placeholder">
          Add images to build a gallery
        </span>
      </figure>
    );
  }

  return (
    <figure className={className} data-block="core/gallery">
      {images.map((img, idx) => (
        <figure className="wp-block-image" key={idx}>
          <img
            src={img.url}
            alt={img.alt}
            width={img.width}
            height={img.height}
          />
          {img.caption ? <figcaption>{img.caption}</figcaption> : null}
          {isSelected ? (
            <div className="gn-block-gallery__controls" aria-hidden="false">
              <button
                type="button"
                aria-label={`Move image ${idx + 1} left`}
                disabled={idx === 0}
                onClick={() => move(idx, -1)}
              >
                {'<'}
              </button>
              <button
                type="button"
                aria-label={`Move image ${idx + 1} right`}
                disabled={idx === images.length - 1}
                onClick={() => move(idx, 1)}
              >
                {'>'}
              </button>
            </div>
          ) : null}
        </figure>
      ))}
    </figure>
  );
}

export default GalleryEdit;
