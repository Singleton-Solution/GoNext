'use client';

/**
 * LayoutGridEditor — content + wide width editor with live "this much
 * of the viewport" preview.
 *
 * Replaces the basic LayoutSection's two text inputs with a paired
 * rem/px input set plus a horizontal bar preview that shows each
 * width as a percentage of the operator's current viewport.
 *
 * The text input remains for power users (so a `clamp(...)` value
 * still round-trips), but the rem field is the primary control —
 * matches how WordPress's site-editor exposes contentSize/wideSize
 * in `theme.json` v3.
 */
import { useEffect, useState, type ReactElement } from 'react';
import type { LayoutSettings } from '../types';
import { TokenPreview } from './TokenPreview';

const ROOT_FONT_PX = 16;

export interface LayoutGridEditorProps {
  layout: LayoutSettings;
  onChange: (next: LayoutSettings) => void;
}

export function LayoutGridEditor({
  layout,
  onChange,
}: LayoutGridEditorProps): ReactElement {
  // Track the current viewport so the preview bars can render at the
  // operator's actual screen ratio. SSR-safe via the conditional in
  // `useEffect` — the bars default to a 1440 baseline before mount.
  const [viewportPx, setViewportPx] = useState<number>(1440);

  useEffect(() => {
    if (typeof window === 'undefined') return undefined;
    const update = (): void => setViewportPx(window.innerWidth);
    update();
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, []);

  return (
    <div className="customizer-advanced__panel" data-testid="layout-grid-editor">
      <p className="muted">
        Content width controls the readable bound; wide width sets the
        full-bleed-but-bounded ceiling.
      </p>
      <LayoutField
        label="Content width"
        slug="content"
        path="/settings/layout/contentSize"
        value={layout.contentSize ?? ''}
        onChange={(next) => onChange({ ...layout, contentSize: next })}
        viewportPx={viewportPx}
      />
      <LayoutField
        label="Wide width"
        slug="wide"
        path="/settings/layout/wideSize"
        value={layout.wideSize ?? ''}
        onChange={(next) => onChange({ ...layout, wideSize: next })}
        viewportPx={viewportPx}
      />
    </div>
  );
}

interface LayoutFieldProps {
  label: string;
  slug: string;
  path: string;
  value: string;
  onChange: (next: string) => void;
  viewportPx: number;
}

function LayoutField({
  label,
  slug,
  path,
  value,
  onChange,
  viewportPx,
}: LayoutFieldProps): ReactElement {
  const rem = parseRem(value);
  const px = parsePx(value);
  const resolvedPx = rem !== null ? rem * ROOT_FONT_PX : px;
  const remId = `layout-rem-${slug}`;
  const pxId = `layout-px-${slug}`;

  return (
    <div
      className="layout-grid-editor__row"
      data-path={path}
      data-slug={slug}
    >
      <header className="layout-grid-editor__head">
        <span className="layout-grid-editor__label">{label}</span>
      </header>
      <div className="layout-grid-editor__inputs">
        <div>
          <label htmlFor={remId}>rem</label>
          <input
            id={remId}
            type="number"
            step="0.0625"
            min={0}
            value={rem ?? ''}
            onChange={(e) =>
              onChange(e.target.value === '' ? '' : `${e.target.value}rem`)
            }
            aria-label={`${label} in rem`}
            data-testid={`layout-${slug}-rem`}
          />
        </div>
        <div>
          <label htmlFor={pxId}>px (read-only)</label>
          <input
            id={pxId}
            type="number"
            readOnly
            value={resolvedPx ?? ''}
            aria-label={`${label} in pixels`}
            data-testid={`layout-${slug}-px`}
          />
        </div>
      </div>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-label={`${label} raw CSS value`}
        data-testid={`layout-${slug}-raw`}
        placeholder="e.g. 720px or 45rem"
        spellCheck={false}
      />
      <TokenPreview
        slug={slug}
        label={label}
        detail={value || '—'}
        mode="bar"
        px={resolvedPx ?? 0}
        viewportPx={viewportPx}
      />
    </div>
  );
}

/** Parse a CSS length expressed in `rem`. Returns null for any other
 *  unit so the rem input shows blank rather than a wrong number. */
export function parseRem(raw: string): number | null {
  const m = raw.match(/^(-?\d+(?:\.\d+)?)rem$/);
  if (!m || !m[1]) return null;
  return Number.parseFloat(m[1]);
}

/** Parse a CSS length expressed in `px`. */
export function parsePx(raw: string): number | null {
  const m = raw.match(/^(-?\d+(?:\.\d+)?)px$/);
  if (!m || !m[1]) return null;
  return Number.parseFloat(m[1]);
}

/** Convert rem → px at the canonical 16px root. Exposed for tests. */
export function remToPx(rem: number): number {
  return rem * ROOT_FONT_PX;
}

/** Convert px → rem at the canonical 16px root. */
export function pxToRem(px: number): number {
  return px / ROOT_FONT_PX;
}

export const __testing = { parseRem, parsePx, remToPx, pxToRem };
