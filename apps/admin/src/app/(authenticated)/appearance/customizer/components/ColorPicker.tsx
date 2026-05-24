'use client';

/**
 * ColorPicker — one row in the palette editor.
 *
 * Renders the palette slug + name as read-only context (operators
 * editing colors should not rename slugs — the renderer keys CSS
 * custom properties off them) and the color value as both a native
 * `<input type="color">` swatch and a free-form text field. The two
 * controls share state: typing `#ff` updates the swatch, picking a
 * color through the OS picker updates the text field.
 *
 * The path prop is the JSON pointer the test harness uses to verify
 * the change reaches the right node (e.g.
 * `/settings/color/palette/0/color`). The component does not write
 * the path itself — it surfaces it via a `data-path` attribute so the
 * test can scope its assertions.
 */
import { useId, type ReactElement } from 'react';
import type { ColorEntry } from '../types';

export interface ColorPickerProps {
  entry: ColorEntry;
  /** JSON-pointer-style path into the override payload. Surfaced as
   *  data-path on the wrapper so tests can scope assertions. */
  path: string;
  /** Called with the next ColorEntry whenever any field changes. The
   *  parent owns the palette array. */
  onChange: (next: ColorEntry) => void;
}

export function ColorPicker({ entry, path, onChange }: ColorPickerProps): ReactElement {
  const id = useId();
  const colorId = `${id}-color`;
  const textId = `${id}-text`;

  return (
    <div className="customizer-color-row" data-path={path}>
      <label htmlFor={colorId} className="customizer-color-row__label">
        <span className="customizer-color-row__slug">{entry.slug}</span>
        <span className="customizer-color-row__name">{entry.name}</span>
      </label>
      <input
        id={colorId}
        type="color"
        value={normaliseHex(entry.color)}
        onChange={(e) => onChange({ ...entry, color: e.target.value })}
        aria-label={`${entry.name} color swatch`}
        data-testid={`color-swatch-${entry.slug}`}
      />
      <input
        id={textId}
        type="text"
        value={entry.color}
        onChange={(e) => onChange({ ...entry, color: e.target.value })}
        aria-label={`${entry.name} color value`}
        data-testid={`color-text-${entry.slug}`}
        spellCheck={false}
      />
    </div>
  );
}

/** `<input type="color">` only accepts `#rrggbb`. When the underlying
 *  value is `#rgb`, `rgb(...)`, a named color, or `var(--token)` we
 *  default the swatch to `#000000` rather than disabling the swatch
 *  outright — the text field carries the authoritative value and the
 *  swatch is a convenience for hex authors. */
function normaliseHex(raw: string): string {
  if (/^#[0-9a-fA-F]{6}$/.test(raw)) return raw.toLowerCase();
  if (/^#[0-9a-fA-F]{3}$/.test(raw)) {
    // Expand #rgb -> #rrggbb so the native picker doesn't show "black"
    // unintentionally for valid short hex inputs.
    const [, r, g, b] = raw.match(/^#([0-9a-fA-F])([0-9a-fA-F])([0-9a-fA-F])$/) ?? [];
    return `#${r}${r}${g}${g}${b}${b}`.toLowerCase();
  }
  return '#000000';
}
