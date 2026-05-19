'use client';

/**
 * TypographySection — font family + font size editors.
 *
 * Two sub-panels:
 *  - Font families: per-family `fontFamily` textfield (the CSS
 *    declaration). Slug + Name are surfaced read-only so operators
 *    don't accidentally rename a slug the renderer's CSS variables
 *    depend on.
 *  - Font sizes: a slider per entry that nudges the size up/down in
 *    rem with a min/max envelope. The size value is mirrored as a
 *    text input so power users can type `clamp(...)` or named CSS
 *    units directly.
 */
import { type ReactElement } from 'react';
import type { FontFamily, FontSize } from '../types';

const SIZE_MIN = 0.5;
const SIZE_MAX = 4;
const SIZE_STEP = 0.0625;

export interface TypographySectionProps {
  fontFamilies: FontFamily[];
  fontSizes: FontSize[];
  onFontFamilyChange: (index: number, next: FontFamily) => void;
  onFontSizeChange: (index: number, next: FontSize) => void;
}

export function TypographySection({
  fontFamilies,
  fontSizes,
  onFontFamilyChange,
  onFontSizeChange,
}: TypographySectionProps): ReactElement {
  return (
    <section className="customizer-section" data-testid="customizer-typography">
      <h2 className="customizer-section__title">Typography</h2>

      <h3 className="customizer-subsection__title">Font families</h3>
      <div className="customizer-family-list">
        {fontFamilies.map((fam, i) => (
          <div
            key={fam.slug}
            className="customizer-family-row"
            data-path={`/settings/typography/fontFamilies/${i}`}
          >
            <div className="customizer-family-row__meta">
              <span className="customizer-family-row__slug">{fam.slug}</span>
              <span className="customizer-family-row__name">{fam.name}</span>
            </div>
            <input
              type="text"
              value={fam.fontFamily}
              onChange={(e) =>
                onFontFamilyChange(i, { ...fam, fontFamily: e.target.value })
              }
              aria-label={`${fam.name} font family CSS value`}
              data-testid={`font-family-${fam.slug}`}
              spellCheck={false}
            />
          </div>
        ))}
        {fontFamilies.length === 0 && (
          <p className="muted">No font families declared in this theme.</p>
        )}
      </div>

      <h3 className="customizer-subsection__title">Font sizes</h3>
      <div className="customizer-size-list">
        {fontSizes.map((sz, i) => {
          const numeric = parseRem(sz.size);
          return (
            <div
              key={sz.slug}
              className="customizer-size-row"
              data-path={`/settings/typography/fontSizes/${i}`}
            >
              <label
                className="customizer-size-row__label"
                htmlFor={`font-size-slider-${sz.slug}`}
              >
                <span className="customizer-size-row__slug">{sz.slug}</span>
                <span className="customizer-size-row__name">{sz.name}</span>
              </label>
              <input
                id={`font-size-slider-${sz.slug}`}
                type="range"
                min={SIZE_MIN}
                max={SIZE_MAX}
                step={SIZE_STEP}
                value={numeric ?? SIZE_MIN}
                disabled={numeric === null}
                onChange={(e) =>
                  onFontSizeChange(i, { ...sz, size: `${e.target.value}rem` })
                }
                aria-label={`${sz.name} font size slider`}
                data-testid={`font-size-slider-${sz.slug}`}
              />
              <input
                type="text"
                value={sz.size}
                onChange={(e) => onFontSizeChange(i, { ...sz, size: e.target.value })}
                aria-label={`${sz.name} font size value`}
                data-testid={`font-size-text-${sz.slug}`}
                spellCheck={false}
              />
            </div>
          );
        })}
        {fontSizes.length === 0 && (
          <p className="muted">No font sizes declared in this theme.</p>
        )}
      </div>
    </section>
  );
}

/** Parse a CSS length expressed in rem. Returns null for values the
 *  slider cannot represent (e.g. `clamp(...)`, `vw`, named keywords).
 *  The slider is disabled for those entries — the text field still
 *  works for power users. */
function parseRem(s: string): number | null {
  const m = s.match(/^(-?\d+(?:\.\d+)?)rem$/);
  if (!m || !m[1]) return null;
  return Number.parseFloat(m[1]);
}
