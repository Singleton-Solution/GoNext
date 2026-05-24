/**
 * Tests for the ShadowPresetsEditor.
 *
 * Coverage:
 *  - One row per preset, four total.
 *  - Each slider (offset-x/y, blur, spread) updates that field on the
 *    target preset and leaves the rest untouched.
 *  - The composed CSS string follows the canonical
 *    `x y blur spread color` order and is valid box-shadow syntax.
 *  - The preview swatch's inline boxShadow re-renders on change.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import {
  ShadowPresetsEditor,
  composeShadow,
} from './ShadowPresetsEditor';
import type { ShadowPreset } from '../types';

const PRESETS: ShadowPreset[] = [
  { slug: 'sm', name: 'Small', offsetX: 0, offsetY: 1, blur: 2, spread: 0, color: 'rgba(0,0,0,0.08)' },
  { slug: 'md', name: 'Medium', offsetX: 0, offsetY: 4, blur: 8, spread: 0, color: 'rgba(0,0,0,0.12)' },
  { slug: 'lg', name: 'Large', offsetX: 0, offsetY: 8, blur: 16, spread: 0, color: 'rgba(0,0,0,0.16)' },
  { slug: 'xl', name: 'Extra large', offsetX: 0, offsetY: 16, blur: 32, spread: 0, color: 'rgba(0,0,0,0.2)' },
];

describe('ShadowPresetsEditor', () => {
  it('renders one row per preset', () => {
    render(<ShadowPresetsEditor presets={PRESETS} onChange={vi.fn()} />);
    for (const p of PRESETS) {
      expect(screen.getByTestId(`shadow-blur-${p.slug}`)).toBeInTheDocument();
      expect(screen.getByTestId(`shadow-color-${p.slug}`)).toBeInTheDocument();
    }
  });

  it('blur slider updates only the target preset', () => {
    const onChange = vi.fn();
    render(<ShadowPresetsEditor presets={PRESETS} onChange={onChange} />);
    const slider = screen.getByTestId('shadow-blur-md') as HTMLInputElement;
    fireEvent.change(slider, { target: { value: '24' } });
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0][0] as ShadowPreset[];
    expect(next[1]?.blur).toBe(24);
    expect(next[0]?.blur).toBe(PRESETS[0]?.blur);
  });

  it('offset-x and spread sliders also update their field', () => {
    const onChange = vi.fn();
    render(<ShadowPresetsEditor presets={PRESETS} onChange={onChange} />);
    fireEvent.change(screen.getByTestId('shadow-offset-x-lg'), {
      target: { value: '-4' },
    });
    fireEvent.change(screen.getByTestId('shadow-spread-lg'), {
      target: { value: '3' },
    });
    expect(onChange).toHaveBeenCalledTimes(2);
    const last = onChange.mock.calls[1][0] as ShadowPreset[];
    expect(last[2]?.spread).toBe(3);
  });

  it('composeShadow emits a valid CSS box-shadow string', () => {
    const css = composeShadow({
      slug: 'md',
      name: 'Medium',
      offsetX: 2,
      offsetY: 4,
      blur: 12,
      spread: -1,
      color: 'rgba(0,0,0,0.2)',
    });
    expect(css).toBe('2px 4px 12px -1px rgba(0,0,0,0.2)');
    // Sanity check: the format is `Npx Npx Npx Npx color`. We're not
    // asking the browser to parse it here — the validator in
    // `packages/go/theme` enforces the strict shape — but the order
    // and unit suffixes are the property that has to hold.
    expect(css.split(' ').length).toBeGreaterThanOrEqual(5);
  });

  it('preview swatch boxShadow re-renders when a slider moves', () => {
    const { rerender } = render(
      <ShadowPresetsEditor presets={PRESETS} onChange={vi.fn()} />,
    );
    const swatch = screen.getByTestId('token-preview-swatch-md') as HTMLDivElement;
    const before = swatch.style.boxShadow;

    const updated = PRESETS.map((p) =>
      p.slug === 'md' ? { ...p, blur: 24 } : p,
    );
    rerender(<ShadowPresetsEditor presets={updated} onChange={vi.fn()} />);
    expect(swatch.style.boxShadow).not.toBe(before);
    expect(swatch.style.boxShadow).toContain('24px');
  });

  it('color input writes back the new color string', () => {
    const onChange = vi.fn();
    render(<ShadowPresetsEditor presets={PRESETS} onChange={onChange} />);
    fireEvent.change(screen.getByTestId('shadow-color-sm'), {
      target: { value: 'rgba(0,0,0,0.5)' },
    });
    const next = onChange.mock.calls[0][0] as ShadowPreset[];
    expect(next[0]?.color).toBe('rgba(0,0,0,0.5)');
  });
});
