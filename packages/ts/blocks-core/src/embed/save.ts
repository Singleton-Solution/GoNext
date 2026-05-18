/**
 * `core/embed` save serializer + server-render hint.
 *
 * Embeds render the canonical wrapper that lets theme CSS implement
 * responsive aspect-ratio reservations: an outer `<figure>` carrying the
 * `wp-block-embed` class plus provider / aspect modifiers, and an inner
 * `<div class="wp-block-embed__wrapper">` housing the embedded payload.
 *
 * The payload — `{html}` — comes from the oEmbed lookup. That lookup is
 * not yet wired (see TODO in the README), so server-side rendering falls
 * back to surfacing the raw URL inside the wrapper. Once the oEmbed
 * cache lands, only `serverRender` changes; the persisted shape stays
 * the same.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';
import { detectProvider } from './providers.ts';

/** Attribute shape for `core/embed`. */
export interface EmbedAttributes extends BlockAttributes {
  /** The embedded resource URL. */
  url: string;
  /**
   * Cached provider slug. Optional — if missing we recompute via
   * `detectProvider`. The cache exists so the editor doesn't re-run
   * regexes on every render.
   */
  providerNameSlug?: string;
  /** Responsive aspect-ratio reservation. Defaults to true. */
  responsive?: boolean;
  /** Aspect ratio token (e.g. `16-9`). */
  aspectRatio?: string;
}

function embedClasses(attrs: EmbedAttributes): string[] {
  // Trust the persisted slug, but fall back to detection if absent so
  // newly inserted blocks still pick the right modifier class before
  // the inspector has had a chance to write the slug back.
  const slug =
    attrs.providerNameSlug ?? detectProvider(attrs.url)?.slug ?? null;
  return [
    'wp-block-embed',
    slug ? `is-provider-${slug}` : null,
    slug ? `wp-block-embed-${slug}` : null,
    attrs.responsive !== false ? 'is-responsive' : null,
    attrs.aspectRatio ? `wp-embed-aspect-${attrs.aspectRatio}` : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Pure serializer. The wrapper `<figure>` carries the class list; the
 * inner `<div class="wp-block-embed__wrapper">` hosts the payload. We
 * emit the raw URL as the payload here — the editor's full save pipeline
 * substitutes the oEmbed HTML when available.
 */
export function save({ attributes }: BlockSaveProps<EmbedAttributes>): string {
  const wrapped = escapeHtml(attributes.url);
  return `<figure${classAttr(embedClasses(attributes))}><div class="wp-block-embed__wrapper">${wrapped}</div></figure>`;
}

export function serverRender(
  attrs: EmbedAttributes,
  innerHtml: string,
): string {
  // The walker passes `innerHtml` empty for leaf blocks. For embeds the
  // server-side path will eventually pass the oEmbed-resolved HTML in
  // through `innerHtml`; until then we fall through to `save()` so the
  // bytes match for the URL-only case.
  if (innerHtml.length > 0) {
    return `<figure${classAttr(embedClasses(attrs))}><div class="wp-block-embed__wrapper">${innerHtml}</div></figure>`;
  }
  return save({ attributes: attrs });
}
