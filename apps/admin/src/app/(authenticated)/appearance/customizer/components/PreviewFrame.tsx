'use client';

/**
 * PreviewFrame — live preview iframe to the public site.
 *
 * The iframe initially loads
 * `<publicSiteUrl>?customizer=preview&overrides=<base64>` so the public
 * renderer rebuilds the CSS-variable map from the encoded blob on first
 * paint. This keeps preview inspectable from devtools: open the iframe
 * URL in a new tab and the same overrides apply, no parent app needed.
 *
 * On top of that one-shot load we now publish a postMessage stream so
 * subsequent edits in the parent customizer apply *without* a full
 * reload. The cadence — every keystroke or slider drag — is too tight
 * for a navigation cycle to feel live, especially on slow connections.
 * The renderer-side shim listens for these messages and patches its
 * inline CSS variables in place; if the shim isn't yet wired the
 * iframe simply ignores the message and the URL-encoded overrides keep
 * acting as the source of truth.
 *
 * ─── Message schema ─────────────────────────────────────────────────
 *
 * Direction: parent → child (the customizer admin → the public site
 *            inside the iframe).
 *
 * Channel:   {@link CUSTOMIZER_PREVIEW_CHANNEL} (`"gonext.customizer"`)
 *            on `message.data.channel`. Messages without this discriminator
 *            are ignored on both sides — keeps the shim safe to mount
 *            on any page even if a third-party widget posts unrelated
 *            messages.
 *
 * Origin:    Same-origin only. The parent calls
 *            `iframe.contentWindow.postMessage(payload, expectedOrigin)`
 *            with `expectedOrigin` derived from `publicSiteUrl`, so the
 *            browser refuses to deliver the message if the iframe ever
 *            navigated to a different origin (mitigates a cross-origin
 *            leak of theme overrides — they're not secrets, but the
 *            same posture protects future capability payloads).
 *
 * Schema (TypeScript shape, version-tagged):
 *
 *   {
 *     channel: 'gonext.customizer',
 *     type: 'overrides:update',
 *     version: 1,
 *     overrides: ThemeOverrides,
 *   }
 *
 * Future message types are expected to share the same envelope (channel
 * + version) so the renderer-side switch is by `type`. The version field
 * gates breaking schema changes — a v2 message a v1 shim doesn't
 * recognise should still be safely ignored.
 *
 * ─── Why postMessage and not a re-navigate ──────────────────────────
 *
 * The previous shape (re-navigation per edit) was correct for the
 * stateless renderer shim but cost an entire page lifecycle per
 * keystroke — script re-eval, image refetch, scroll position lost.
 * postMessage delivers the delta to a still-loaded page and lets the
 * shim animate or merge changes; the renderer can fall back to a
 * full reload by checking `version` if it ever finds itself behind a
 * payload it can't apply.
 */
import {
  useEffect,
  useMemo,
  useRef,
  type ReactElement,
} from 'react';
import { previewUrl } from '../state';
import type { ThemeOverrides } from '../types';

/** Discriminator both ends check on `message.data.channel`. Picked so
 *  the message is unambiguously ours even on a page hosting other
 *  postMessage senders (analytics SDKs, embedded widgets, etc.). */
export const CUSTOMIZER_PREVIEW_CHANNEL = 'gonext.customizer';

/** Current message-envelope version. Bump only on a breaking change. */
export const CUSTOMIZER_PREVIEW_VERSION = 1 as const;

/** Live-update message: every customizer edit produces one of these. */
export interface CustomizerPreviewUpdateMessage {
  channel: typeof CUSTOMIZER_PREVIEW_CHANNEL;
  type: 'overrides:update';
  version: typeof CUSTOMIZER_PREVIEW_VERSION;
  overrides: ThemeOverrides;
}

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
  // Initial src derives from the inputs. useMemo keeps the URL stable
  // across renders that don't actually change the override — without
  // it the iframe would refetch on every keystroke that didn't touch
  // the customizer state.
  //
  // Once the postMessage bridge below takes over, this src only
  // matters on first paint and on coarse navigations (different
  // previewPath, frameWidth change). Keystroke-grained edits flow
  // through the message channel instead.
  const src = useMemo(
    () => previewUrl(joinUrl(publicSiteUrl, previewPath), overrides),
    [publicSiteUrl, previewPath, overrides],
  );

  // When the breakpoint editor locks the preview to a specific width,
  // both the iframe's HTML `width` attribute and its CSS width have to
  // pin. We surface the width as a data attribute too so tests can
  // assert without relying on style parsing.
  const widthAttr = frameWidth && frameWidth > 0 ? frameWidth : undefined;

  const iframeRef = useRef<HTMLIFrameElement | null>(null);

  // Stable origin derived from the configured public site URL. The
  // browser refuses to deliver the postMessage if the iframe ever
  // navigates to a different origin — this is the "same-origin
  // allowlist" called out in the brief, expressed as the second arg
  // to postMessage rather than a parsed string-match.
  const expectedOrigin = useMemo(
    () => safeOrigin(publicSiteUrl),
    [publicSiteUrl],
  );

  // Publish a live-update message whenever `overrides` changes. The
  // effect runs once per render where the overrides reference flipped;
  // CustomizerClient already memoizes that value so this fires once
  // per logical edit, not once per keystroke through React tree
  // updates that don't touch the override map.
  //
  // The first render also fires a message, which is intentional — the
  // iframe might still be mid-load, in which case the renderer-side
  // shim queues the latest payload and applies it on its DOMContentLoaded.
  useEffect(() => {
    const frame = iframeRef.current;
    if (frame === null) return;
    const target = frame.contentWindow;
    if (target === null) return;
    if (expectedOrigin === null) return; // bad publicSiteUrl, fall back to URL re-render

    const message: CustomizerPreviewUpdateMessage = {
      channel: CUSTOMIZER_PREVIEW_CHANNEL,
      type: 'overrides:update',
      version: CUSTOMIZER_PREVIEW_VERSION,
      overrides,
    };
    try {
      target.postMessage(message, expectedOrigin);
    } catch {
      // postMessage throws TypeError if the structured-clone fails
      // (e.g. a cyclic object slipped into overrides). Swallow — the
      // URL-encoded fallback already covers the user-visible state,
      // and we don't want to break the customizer over a serialise
      // hiccup. A console.warn would be noise during normal edits.
    }
  }, [overrides, expectedOrigin]);

  return (
    <div className="customizer-preview" data-frame-width={widthAttr ?? 'full'}>
      <iframe
        ref={iframeRef}
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

/** Pull the origin out of a URL, or return null when the value isn't a
 *  parseable absolute URL. The caller treats null as "skip the
 *  postMessage send" so a misconfigured publicSiteUrl falls back to
 *  the URL-encoded preview path without crashing the admin. */
function safeOrigin(url: string): string | null {
  try {
    return new URL(url).origin;
  } catch {
    return null;
  }
}
