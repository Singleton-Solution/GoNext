/**
 * `core/video` Edit component.
 *
 * Shows a placeholder slot when no `src` is set yet. Once a `src` exists,
 * we render the same `<video>` element that `save()` emits so the canvas
 * preview matches the rendered page. We never set `autoplay` in the
 * editor — playing media on focus inside an authoring surface is
 * notoriously disruptive — but other flags carry through to the DOM.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { VideoAttributes } from './save.ts';

export function VideoEdit({
  attributes,
  isSelected,
}: BlockEditProps<VideoAttributes>): ReactElement {
  const className = ['gn-block-video', isSelected ? 'is-selected' : null]
    .filter((c): c is string => c !== null)
    .join(' ');

  if (!attributes.src) {
    return (
      <figure className={className} data-block="core/video">
        <span className="gn-block-video__placeholder">
          Pick or upload a video
        </span>
      </figure>
    );
  }

  return (
    <figure className={className} data-block="core/video">
      <video
        src={attributes.src}
        poster={attributes.poster}
        controls={attributes.controls !== false}
        // Deliberately omit autoplay in the editor canvas.
        loop={attributes.loop}
        muted={attributes.muted}
      />
      {attributes.caption ? <figcaption>{attributes.caption}</figcaption> : null}
    </figure>
  );
}

export default VideoEdit;
