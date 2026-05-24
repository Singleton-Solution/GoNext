'use client';

/**
 * ShadowPresetsEditor — advanced editor for the four shadow presets
 * (sm, md, lg, xl).
 *
 * Each preset is broken into its CSS box-shadow components:
 *  - offset-x / offset-y : horizontal/vertical displacement (px)
 *  - blur : softness of the shadow edge (px)
 *  - spread : positive grows the shadow, negative shrinks (px)
 *  - color : the shadow color (typically a low-opacity rgba)
 *
 * Splitting into sliders rather than a single text input means the
 * operator can tune one knob at a time without re-typing the
 * surrounding syntax — same UX as Figma's effect panel. The live
 * preview swatch shows the recomposed CSS shadow next to each row.
 */
import { type ReactElement } from 'react';
import type { ShadowPreset } from '../types';
import { TokenPreview } from './TokenPreview';

const OFFSET_MIN = -32;
const OFFSET_MAX = 32;
const BLUR_MIN = 0;
const BLUR_MAX = 64;
const SPREAD_MIN = -16;
const SPREAD_MAX = 16;

export interface ShadowPresetsEditorProps {
  presets: ShadowPreset[];
  onChange: (next: ShadowPreset[]) => void;
}

export function ShadowPresetsEditor({
  presets,
  onChange,
}: ShadowPresetsEditorProps): ReactElement {
  function updatePreset(index: number, next: ShadowPreset): void {
    const updated = presets.slice();
    updated[index] = next;
    onChange(updated);
  }

  return (
    <div className="customizer-advanced__panel" data-testid="shadow-presets-editor">
      <p className="muted">
        Adjust offset, blur, spread, and color. Each preset emits a single CSS
        box-shadow value.
      </p>
      <div className="shadow-presets-editor__list">
        {presets.map((preset, i) => {
          const css = composeShadow(preset);
          return (
            <div
              key={preset.slug}
              className="shadow-presets-editor__row"
              data-path={`/settings/custom/gonext/shadowPresets/${i}`}
              data-slug={preset.slug}
            >
              <header className="shadow-presets-editor__head">
                <span className="shadow-presets-editor__slug">{preset.slug}</span>
                <span className="shadow-presets-editor__name">{preset.name}</span>
              </header>
              <ShadowSlider
                label="Offset X"
                slug={preset.slug}
                field="offset-x"
                min={OFFSET_MIN}
                max={OFFSET_MAX}
                value={preset.offsetX}
                onChange={(value) => updatePreset(i, { ...preset, offsetX: value })}
              />
              <ShadowSlider
                label="Offset Y"
                slug={preset.slug}
                field="offset-y"
                min={OFFSET_MIN}
                max={OFFSET_MAX}
                value={preset.offsetY}
                onChange={(value) => updatePreset(i, { ...preset, offsetY: value })}
              />
              <ShadowSlider
                label="Blur"
                slug={preset.slug}
                field="blur"
                min={BLUR_MIN}
                max={BLUR_MAX}
                value={preset.blur}
                onChange={(value) => updatePreset(i, { ...preset, blur: value })}
              />
              <ShadowSlider
                label="Spread"
                slug={preset.slug}
                field="spread"
                min={SPREAD_MIN}
                max={SPREAD_MAX}
                value={preset.spread}
                onChange={(value) => updatePreset(i, { ...preset, spread: value })}
              />
              <div className="shadow-presets-editor__color">
                <label htmlFor={`shadow-color-${preset.slug}`}>Color</label>
                <input
                  id={`shadow-color-${preset.slug}`}
                  type="text"
                  value={preset.color}
                  onChange={(e) => updatePreset(i, { ...preset, color: e.target.value })}
                  aria-label={`${preset.name} shadow color`}
                  data-testid={`shadow-color-${preset.slug}`}
                  spellCheck={false}
                />
              </div>
              <TokenPreview
                slug={preset.slug}
                label={preset.slug}
                detail={css}
                mode="shadow"
                shadow={css}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

interface ShadowSliderProps {
  label: string;
  slug: string;
  field: string;
  min: number;
  max: number;
  value: number;
  onChange: (value: number) => void;
}

function ShadowSlider({
  label,
  slug,
  field,
  min,
  max,
  value,
  onChange,
}: ShadowSliderProps): ReactElement {
  const id = `shadow-${slug}-${field}`;
  return (
    <div className="shadow-presets-editor__slider">
      <label htmlFor={id}>
        {label} <span className="shadow-presets-editor__readout">{value}px</span>
      </label>
      <input
        id={id}
        type="range"
        min={min}
        max={max}
        step={1}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        aria-label={`${label} for ${slug}`}
        data-testid={`shadow-${field}-${slug}`}
      />
    </div>
  );
}

/** Compose a CSS box-shadow declaration from the preset's pieces.
 *  Format follows the canonical `x y blur spread color` order so the
 *  emitted string is round-trip stable with the parser the theme
 *  validator already uses. */
export function composeShadow(preset: ShadowPreset): string {
  return `${preset.offsetX}px ${preset.offsetY}px ${preset.blur}px ${preset.spread}px ${preset.color}`;
}

export const __testing = { composeShadow };
