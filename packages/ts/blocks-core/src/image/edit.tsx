/**
 * `core/image` Edit component.
 *
 * Renders a placeholder when no `url` has been set yet — the file picker
 * lives in a follow-up issue, but the placeholder slot is wired so the
 * canvas isn't empty.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { ImageAttributes } from './save.ts';

export function ImageEdit({
  attributes,
  isSelected,
}: BlockEditProps<ImageAttributes>): ReactElement {
  const className = [
    'gn-block-image',
    attributes.align ? `align${attributes.align}` : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  if (!attributes.url) {
    return (
      <figure className={className} data-block="core/image">
        <span className="gn-block-image__placeholder">Pick or upload an image</span>
      </figure>
    );
  }

  return (
    <figure className={className} data-block="core/image">
      <img
        src={attributes.url}
        alt={attributes.alt}
        width={attributes.width}
        height={attributes.height}
      />
      {attributes.caption ? <figcaption>{attributes.caption}</figcaption> : null}
    </figure>
  );
}

export default ImageEdit;
