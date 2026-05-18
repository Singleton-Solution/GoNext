/**
 * `core/button` save serializer + server-render hint.
 *
 * Buttons render as `<div class="wp-block-button"><a class="wp-block-
 * button__link">…</a></div>` — the wrapper div lets a button group host
 * multiple sibling buttons without flexbox surprises, and the inner `<a>`
 * carries the actual link semantics so screen readers and crawlers see
 * a real link rather than a clickable div.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/button`. */
export interface ButtonAttributes extends BlockAttributes {
  /** Visible button text. */
  text: string;
  /** Optional href the button links to. */
  url?: string;
  /** Target window for the link. */
  linkTarget?: '_self' | '_blank';
  /** Visual style preset. */
  style?: 'fill' | 'outline';
  /** Border radius in pixels. Theme styles can override. */
  borderRadius?: number;
  /** Alignment within the parent container. */
  align?: 'left' | 'center' | 'right';
}

/** Wrapper `<div class="wp-block-button">` classes. */
function wrapperClasses(attrs: ButtonAttributes): string[] {
  return [
    'wp-block-button',
    attrs.style ? `is-style-${attrs.style}` : null,
    attrs.align ? `has-text-align-${attrs.align}` : null,
  ].filter((c): c is string => c !== null);
}

/**
 * Pure serializer. Renders `<div><a/></div>`. When `url` is unset we still
 * emit the `<a>` (without href) so the editor's "wire me up" prompt shows
 * up consistently in the saved bytes — themes can hide hrefless buttons
 * via CSS.
 */
export function save({ attributes }: BlockSaveProps<ButtonAttributes>): string {
  const target =
    attributes.linkTarget && attributes.linkTarget !== '_self'
      ? ` target="${escapeHtml(attributes.linkTarget)}" rel="noopener noreferrer"`
      : '';
  const href = attributes.url
    ? ` href="${escapeHtml(attributes.url)}"`
    : '';
  const style =
    attributes.borderRadius !== undefined
      ? ` style="${escapeHtml(`border-radius:${attributes.borderRadius}px`)}"`
      : '';
  const anchor = `<a class="wp-block-button__link"${href}${target}${style}>${escapeHtml(attributes.text)}</a>`;
  return `<div${classAttr(wrapperClasses(attributes))}>${anchor}</div>`;
}

export function serverRender(
  attrs: ButtonAttributes,
  _innerHtml: string,
): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
