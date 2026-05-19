'use client';

/**
 * SpacingScaleEditor — advanced editor for the six named spacing
 * tokens (xs, sm, md, lg, xl, 2xl).
 *
 * Each token gets a draggable range slider that drives the
 * underlying rem value. The slider step is fine enough (0.0625 rem
 * ≈ 1px at default browser font size) that the bar feels analog
 * without the input becoming unstable.
 *
 * Power users can drop into the text field next to the slider for
 * arbitrary CSS lengths (`clamp(...)`, `vw`, named keywords). The
 * slider is disabled in that case — same pattern as the basic
 * TypographySection so the surface stays predictable.
 *
 * Preview: each token also renders a `TokenPreview` swatch whose
 * side equals the resolved pixel length, so the operator sees the
 * scale move as they drag.
 */
import { type ReactElement } from 'react';
import type { SpacingSize } from '../types';
import { TokenPreview } from './TokenPreview';

const SLIDER_MIN_REM = 0;
const SLIDER_MAX_REM = 8;
const SLIDER_STEP_REM = 0.0625;
const ROOT_FONT_PX = 16;

export interface SpacingScaleEditorProps {
  tokens: SpacingSize[];
  onChange: (next: SpacingSize[]) => void;
}

export function SpacingScaleEditor({
  tokens,
  onChange,
}: SpacingScaleEditorProps): ReactElement {
  function updateToken(index: number, next: SpacingSize): void {
    const updated = tokens.slice();
    updated[index] = next;
    onChange(updated);
  }

  return (
    <div className="customizer-advanced__panel" data-testid="spacing-scale-editor">
      <p className="muted">
        Drag each token to resize. Preview swatches reflect the resolved pixel
        length at a 16px root.
      </p>
      <div className="spacing-scale-editor__list">
        {tokens.map((token, i) => {
          const rem = parseRemValue(token.size);
          const px = rem === null ? null : Math.round(rem * ROOT_FONT_PX);
          return (
            <div
              key={token.slug}
              className="spacing-scale-editor__row"
              data-path={`/settings/custom/gonext/spacingTokens/${i}`}
              data-slug={token.slug}
            >
              <div className="spacing-scale-editor__meta">
                <span className="spacing-scale-editor__slug">{token.slug}</span>
                <span className="spacing-scale-editor__name">{token.name}</span>
              </div>
              <input
                type="range"
                min={SLIDER_MIN_REM}
                max={SLIDER_MAX_REM}
                step={SLIDER_STEP_REM}
                value={rem ?? SLIDER_MIN_REM}
                disabled={rem === null}
                onChange={(e) =>
                  updateToken(i, { ...token, size: `${e.target.value}rem` })
                }
                aria-label={`${token.name} spacing slider`}
                data-testid={`spacing-token-slider-${token.slug}`}
              />
              <input
                type="text"
                value={token.size}
                onChange={(e) => updateToken(i, { ...token, size: e.target.value })}
                aria-label={`${token.name} spacing value`}
                data-testid={`spacing-token-text-${token.slug}`}
                spellCheck={false}
              />
              <TokenPreview
                slug={token.slug}
                label={token.slug}
                detail={token.size}
                mode="size"
                px={px ?? 0}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** Parse `Nrem` to a number. Returns null for any other unit so the
 *  slider stays inert when the value is a `clamp(...)` or `vw`-based
 *  expression. */
function parseRemValue(raw: string): number | null {
  const m = raw.match(/^(-?\d+(?:\.\d+)?)rem$/);
  if (!m || !m[1]) return null;
  return Number.parseFloat(m[1]);
}

export const __testing = { parseRemValue };
