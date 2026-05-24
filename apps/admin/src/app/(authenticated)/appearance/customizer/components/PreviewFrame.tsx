'use client';

/**
 * PreviewFrame — live preview iframe to the public site.
 *
 * The iframe loads `<publicSiteUrl>?customizer=preview&overrides=<base64>`
 * — the renderer detects the query flag and applies the inline
 * overrides without persisting them. As the operator tweaks the
 * sidebar controls, the URL updates and the iframe re-navigates.
 *
 * We do NOT use postMessage for the preview update because the
 * renderer's preview shim is intentionally stateless — it expects the
 * overrides on every load and rebuilds the CSS variable map from
 * scratch. That keeps the preview surface inspectable from devtools
 * without a runtime message handler.
 */
import { useMemo, type ReactElement } from 'react';
import { previewUrl } from '../state';
import type { ThemeOverrides } from '../types';

export interface PreviewFrameProps {
  /** Absolute URL to the public site root, e.g. http://localhost:3000. */
  publicSiteUrl: string;
  /** Current (unsaved) overrides — the value the iframe should render. */
  overrides: ThemeOverrides;
  /** Optional path to preview inside the public site. Defaults to /. */
  previewPath?: string;
  /** Optional fixed pixel width for the iframe. Drives the
   *  responsive preview when the BreakpointEditor locks the iframe to
   *  a specific viewport. */
  frameWidth?: number | null;
}

export function PreviewFrame({
  publicSiteUrl,
  overrides,
  previewPath = '/',
  frameWidth = null,
}: PreviewFrameProps): ReactElement {
  // Derive the iframe src from the inputs. useMemo keeps the URL
  // stable across renders that don't actually change the override —
  // without it the iframe would refetch on every keystroke that
  // didn't touch the customizer state.
  const src = useMemo(
    () => previewUrl(joinUrl(publicSiteUrl, previewPath), overrides),
    [publicSiteUrl, previewPath, overrides],
  );

  // When the breakpoint editor locks the preview to a specific width,
  // both the iframe's HTML `width` attribute and its CSS width have to
  // pin. We surface the width as a data attribute too so tests can
  // assert without relying on style parsing.
  const widthAttr = frameWidth && frameWidth > 0 ? frameWidth : undefined;

  return (
    <div className="customizer-preview" data-frame-width={widthAttr ?? 'full'}>
      <iframe
        title="Theme preview"
        src={src}
        className="customizer-preview__frame"
        data-testid="customizer-preview-frame"
        {...(widthAttr ? { width: widthAttr, style: { width: `${widthAttr}px` } } : {})}
      />
    </div>
  );
}

function joinUrl(base: string, path: string): string {
  if (!path) return base;
  const left = base.endsWith('/') ? base.slice(0, -1) : base;
  const right = path.startsWith('/') ? path : `/${path}`;
  return `${left}${right}`;
}
