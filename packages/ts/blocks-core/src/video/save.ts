/**
 * `core/video` save serializer + server-render hint.
 *
 * Videos render as `<figure><video>...</video></figure>` so the wrapper
 * can host an optional caption inside `<figcaption>`. The playback flags
 * (`controls`, `autoplay`, `loop`, `muted`) are emitted as the HTML
 * boolean attributes the browser already understands — no JS shim needed.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/video`. */
export interface VideoAttributes extends BlockAttributes {
  /** Video URL. */
  src: string;
  /** Optional poster image shown before playback. */
  poster?: string;
  /** Show native player controls. Defaults to true. */
  controls?: boolean;
  /** Autoplay on load. Most browsers require `muted=true` too. */
  autoplay?: boolean;
  /** Loop playback. */
  loop?: boolean;
  /** Mute audio on load. */
  muted?: boolean;
  /** Optional caption rendered inside `<figcaption>`. */
  caption?: string;
}

function videoClasses(_attrs: VideoAttributes): string[] {
  return ['gn-block-video'];
}

/**
 * Emit boolean HTML attributes the browser respects natively. `controls`
 * defaults to `true` so unspecified videos still get a usable player.
 */
function booleanAttrs(attrs: VideoAttributes): string {
  const flags: string[] = [];
  if (attrs.controls !== false) flags.push('controls');
  if (attrs.autoplay) flags.push('autoplay');
  if (attrs.loop) flags.push('loop');
  if (attrs.muted) flags.push('muted');
  return flags.length === 0 ? '' : ' ' + flags.join(' ');
}

/**
 * Pure serializer. The wrapping `<figure>` carries the block class so
 * theme CSS can target it; the inner `<video>` carries source + flags
 * only.
 */
export function save({ attributes }: BlockSaveProps<VideoAttributes>): string {
  const poster = attributes.poster
    ? ` poster="${escapeHtml(attributes.poster)}"`
    : '';
  const video = `<video src="${escapeHtml(attributes.src)}"${poster}${booleanAttrs(attributes)}></video>`;
  const caption = attributes.caption
    ? `<figcaption>${escapeHtml(attributes.caption)}</figcaption>`
    : '';
  return `<figure${classAttr(videoClasses(attributes))}>${video}${caption}</figure>`;
}

export function serverRender(
  attrs: VideoAttributes,
  _innerHtml: string,
): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
