/**
 * `core/embed` Edit component.
 *
 * When the block has no URL we show a placeholder slot. When a URL is
 * present we mirror the saved markup: an outer `<figure>` with the
 * provider modifier class, and an inner `<div class="wp-block-
 * embed__wrapper">` showing the URL as a clickable preview. The full
 * oEmbed preview lands once the lookup is wired.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { EmbedAttributes } from './save.ts';
import { detectProvider } from './providers.ts';

export function EmbedEdit({
  attributes,
  isSelected,
}: BlockEditProps<EmbedAttributes>): ReactElement {
  const slug =
    attributes.providerNameSlug ?? detectProvider(attributes.url)?.slug ?? null;
  const className = [
    'wp-block-embed',
    slug ? `is-provider-${slug}` : null,
    slug ? `wp-block-embed-${slug}` : null,
    attributes.responsive !== false ? 'is-responsive' : null,
    attributes.aspectRatio ? `wp-embed-aspect-${attributes.aspectRatio}` : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  if (!attributes.url) {
    return (
      <figure className={className} data-block="core/embed">
        <span className="gn-block-embed__placeholder">
          Paste a link to embed
        </span>
      </figure>
    );
  }

  return (
    <figure className={className} data-block="core/embed">
      <div className="wp-block-embed__wrapper">
        <a href={attributes.url} target="_blank" rel="noopener noreferrer">
          {attributes.url}
        </a>
      </div>
    </figure>
  );
}

export default EmbedEdit;
