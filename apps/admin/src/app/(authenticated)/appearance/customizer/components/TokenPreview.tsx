'use client';

/**
 * TokenPreview — generic preview swatch used by the advanced editors.
 *
 * Three render modes:
 *  - `mode="size"` paints a square whose side equals the resolved
 *    pixel length. Used by SpacingScaleEditor to show each spacing
 *    token as a proportionally sized block.
 *  - `mode="shadow"` paints a card-shaped tile whose `box-shadow` is
 *    the preview value. Used by ShadowPresetsEditor.
 *  - `mode="bar"` paints a horizontal bar whose width equals the
 *    resolved pixel length. Used by LayoutGridEditor (content vs
 *    wide) and BreakpointEditor.
 *
 * The component is intentionally style-light: it ships its own
 * minimal layout (label above, swatch below) and lets the parent
 * decide grid placement. The swatch container itself is a plain div
 * with an inline style so the value is observable from the DOM in
 * tests without parsing computed CSS.
 */
import { type CSSProperties, type ReactElement } from 'react';

export type TokenPreviewMode = 'size' | 'shadow' | 'bar';

export interface TokenPreviewProps {
  /** Stable token identifier surfaced in a `data-slug` attribute so
   *  tests can scope their assertions without going through label
   *  text. */
  slug: string;
  /** Human label rendered above the swatch. */
  label: string;
  /** Secondary label (typically the raw token value, e.g. `1.5rem`).
   *  Optional — omit when the swatch is self-describing. */
  detail?: string;
  /** Render mode — picks the swatch geometry. */
  mode: TokenPreviewMode;
  /** Numeric pixel size for `mode="size"` and `mode="bar"`. Ignored
   *  for `mode="shadow"`. The swatch is clamped to a sensible max so
   *  a comically large value can't blow out the layout. */
  px?: number;
  /** Raw CSS box-shadow string for `mode="shadow"`. */
  shadow?: string;
  /** Optional viewport width — used by `mode="bar"` to render the
   *  bar's width as a percentage of the viewport rather than its
   *  raw pixel size. Lets LayoutGridEditor surface the "this much
   *  of the viewport" intuition WP designers expect. */
  viewportPx?: number;
}

const MAX_SIZE_PX = 96;
const MAX_BAR_PX = 280;

export function TokenPreview({
  slug,
  label,
  detail,
  mode,
  px,
  shadow,
  viewportPx,
}: TokenPreviewProps): ReactElement {
  const swatchStyle = swatchStyleFor(mode, { px, shadow, viewportPx });

  return (
    <div className="token-preview" data-slug={slug} data-mode={mode}>
      <div className="token-preview__labels">
        <span className="token-preview__label">{label}</span>
        {detail && <span className="token-preview__detail">{detail}</span>}
      </div>
      <div
        className={`token-preview__swatch token-preview__swatch--${mode}`}
        style={swatchStyle}
        data-testid={`token-preview-swatch-${slug}`}
      />
    </div>
  );
}

function swatchStyleFor(
  mode: TokenPreviewMode,
  opts: { px?: number; shadow?: string; viewportPx?: number },
): CSSProperties {
  if (mode === 'size') {
    const side = clamp(opts.px ?? 0, 4, MAX_SIZE_PX);
    return { width: `${side}px`, height: `${side}px` };
  }
  if (mode === 'shadow') {
    return { boxShadow: opts.shadow ?? 'none' };
  }
  // bar mode
  if (opts.viewportPx && opts.viewportPx > 0 && opts.px && opts.px > 0) {
    const pct = Math.min(100, (opts.px / opts.viewportPx) * 100);
    return { width: `${pct}%` };
  }
  const width = clamp(opts.px ?? 0, 4, MAX_BAR_PX);
  return { width: `${width}px` };
}

function clamp(value: number, min: number, max: number): number {
  if (Number.isNaN(value)) return min;
  if (value < min) return min;
  if (value > max) return max;
  return value;
}
