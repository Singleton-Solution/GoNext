/**
 * `core/media-text` Edit component.
 *
 * Container block. The text column's children are walked by
 * `<BlockEditCanvas>` itself — this component only emits the wrapper +
 * the media `<figure>` so the canvas's recursion has somewhere to attach
 * the text-column children to. The visible split mirrors the saved
 * markup exactly (same classes, same inline `grid-template-columns`)
 * so what authors see equals what gets rendered.
 *
 * Position / fill / alignment toggles surface through the Inspector
 * panel — this Edit only reflects whatever the current attributes say.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { CSSProperties, ReactElement } from 'react';
import {
  normalizeMediaWidth,
  type MediaTextAttributes,
} from './save.ts';

export function MediaTextEdit({
  attributes,
  isSelected,
}: BlockEditProps<MediaTextAttributes>): ReactElement {
  const pos = attributes.mediaPosition === 'right' ? 'right' : 'left';
  const w = normalizeMediaWidth(attributes.mediaWidth);
  const leftWidth = pos === 'right' ? 100 - w : w;

  const className = [
    'gn-block-media-text',
    `is-media-on-the-${pos}`,
    attributes.verticalAlignment
      ? `is-vertically-aligned-${attributes.verticalAlignment}`
      : null,
    attributes.imageFill ? 'has-media-on-the-fill' : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  const style: CSSProperties = {
    gridTemplateColumns: `${leftWidth}% ${100 - leftWidth}%`,
  };

  // The figure renders an empty slot when no image has been set yet so
  // the visible split matches the saved output (which always emits the
  // figure wrapper). Authors get a clear "missing image" placeholder
  // they can click to open the media picker once that flow lands.
  const figure = (
    <figure className="gn-block-media-text__media">
      {attributes.mediaUrl ? (
        <img src={attributes.mediaUrl} alt={attributes.mediaAlt} />
      ) : (
        <span className="gn-block-media-text__placeholder" aria-hidden="true">
          Add media
        </span>
      )}
      {attributes.mediaCaption ? (
        <figcaption>{attributes.mediaCaption}</figcaption>
      ) : null}
    </figure>
  );

  // Inner-blocks slot — the canvas walker fills this with the text-column
  // children. We keep the marker element so the recursion has a stable
  // anchor even when the text column is empty.
  const content = (
    <div
      className="gn-block-media-text__content"
      data-gn-inner-blocks="content"
    />
  );

  return (
    <div className={className} data-block="core/media-text" style={style}>
      {pos === 'right' ? content : figure}
      {pos === 'right' ? figure : content}
    </div>
  );
}

export default MediaTextEdit;
