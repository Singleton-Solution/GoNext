/**
 * ResponsiveImage — render a responsive <picture> with srcset variants.
 *
 * # Pairing with the server-side image pipeline
 *
 * The server-side image processor in
 * `packages/go/media/imageproc/srcset.go` produces a per-format list of
 * `(url, width)` pairs derived from the original asset (AVIF / WebP /
 * JPEG/PNG fallback). The REST surface emits those as a `variants`
 * payload alongside the canonical `src`. This component is the
 * client-side mirror: it accepts the same shape and renders a `<picture>`
 * with a `<source>` per format and a fallback `<img>` so capable
 * browsers pick AVIF/WebP and older clients fall through.
 *
 * # Why <picture>, not just <img srcset>
 *
 * A single `<img>` with `srcset` can only switch between widths of the
 * SAME format. The server emits multiple formats (AVIF first, then
 * WebP, then a JPEG/PNG fallback), and we want the browser to pick the
 * best decode-supported format. That's what `<picture>` + `<source
 * type="...">` is for.
 *
 * # The author can pass either shape
 *
 * Two ergonomic call sites:
 *   1. Author has only a URL and a list of widths
 *      (the simple case the migrator emits):
 *        <ResponsiveImage src="/uploads/x.jpg" alt="..." widths={[480,1024]} />
 *   2. Author has the full variants payload from the API:
 *        <ResponsiveImage src={src} alt={alt} sources={variants} sizes={sizes} />
 *
 * In case 1 we synthesise a single `<source>` worth of srcset by
 * appending `?w=` query params — the API recognises that shape and
 * negotiates the right variant on first hit, then caches it.
 *
 * # Lazy by default
 *
 * Every instance gets `loading="lazy"` and `decoding="async"` unless
 * the caller explicitly sets `priority` (e.g. a hero image above the
 * fold should be eager). This matches Lighthouse's recommendation for
 * the common case (gallery, post body, listing thumbnails).
 */
import type { CSSProperties, ReactElement } from 'react';

/**
 * One `<source>` entry. `srcset` is already-formatted ("url 480w, url 1024w"),
 * `type` is the MIME type ("image/avif", "image/webp", ...).
 *
 * Matches the wire shape produced by
 * `packages/go/media/imageproc/srcset.go#PictureSource`.
 */
export interface PictureSource {
  srcset: string;
  type: string;
}

export interface ResponsiveImageProps {
  /** Canonical (fallback) URL — what unaware browsers load. */
  src: string;
  /** Alt text. Required; pass "" only for decorative images. */
  alt: string;
  /**
   * Pre-built `<source>` entries — when the caller has the full
   * variants payload from the API. AVIF first, then WebP, then any
   * JPEG/PNG fallbacks.
   */
  sources?: PictureSource[];
  /**
   * Convenience: when the caller only knows widths (e.g. theme author
   * passing `[256, 768, 1536]`), we synthesise a single srcset by
   * appending `?w=` query params to `src`. Ignored if `sources` is set.
   */
  widths?: number[];
  /**
   * The `sizes` attribute. The server provides a sane default
   * (`(max-width: 480px) 256px, (max-width: 1024px) 768px, 1536px`);
   * themes may pass tighter ones based on grid breakpoints.
   */
  sizes?: string;
  /** Intrinsic width — drives the rendered aspect ratio. */
  width?: number;
  /** Intrinsic height — drives the rendered aspect ratio. */
  height?: number;
  /** Treat this image as above-the-fold (no lazy, decode sync). */
  priority?: boolean;
  /** className/style passthrough for the `<img>`. */
  className?: string;
  style?: CSSProperties;
}

const DEFAULT_SIZES = '(max-width: 480px) 256px, (max-width: 1024px) 768px, 1536px';

/**
 * Build a srcset string from a base URL + a list of widths. The
 * resulting URLs use the `?w=N` shape the media API recognises.
 *
 * Exposed for tests; not part of the package's external API.
 */
export function buildWidthSrcSet(src: string, widths: number[]): string {
  if (widths.length === 0) {
    return '';
  }
  const separator = src.includes('?') ? '&' : '?';
  // Sort narrowest-first for determinism — matches the server-side
  // ordering in imageproc.BuildSrcSet.
  const sorted = [...widths].sort((a, b) => a - b);
  return sorted.map((w) => `${src}${separator}w=${w} ${w}w`).join(', ');
}

export function ResponsiveImage({
  src,
  alt,
  sources,
  widths,
  sizes = DEFAULT_SIZES,
  width,
  height,
  priority,
  className,
  style,
}: ResponsiveImageProps): ReactElement {
  // Loading: eager for above-the-fold, lazy otherwise. We don't expose
  // "eager" directly because the only legitimate use is the priority
  // path — eager loading hurts LCP on the common case.
  const loading = priority ? 'eager' : 'lazy';
  // decoding=async lets the browser decode off the main thread; "sync"
  // is only useful for priority paints where the painter is blocked
  // waiting for the decode anyway.
  const decoding = priority ? 'sync' : 'async';

  // The synthesised srcset path. Only used when the caller didn't pass
  // pre-built `sources`.
  const widthSrcSet =
    !sources && widths && widths.length > 0 ? buildWidthSrcSet(src, widths) : undefined;

  return (
    <picture className="gn-responsive-image" data-gn-responsive-image>
      {sources?.map((s) => (
        // The `<source>` order matters: capable browsers pick the FIRST
        // matching one. The server emits AVIF before WebP before
        // JPEG/PNG fallbacks, which is what we want.
        <source key={s.type} srcSet={s.srcset} sizes={sizes} type={s.type} />
      ))}
      {widthSrcSet && <source srcSet={widthSrcSet} sizes={sizes} />}
      <img
        src={src}
        alt={alt}
        width={width}
        height={height}
        loading={loading}
        decoding={decoding}
        className={className}
        style={style}
      />
    </picture>
  );
}
