/**
 * Tests for the LayoutGridEditor.
 *
 * Coverage:
 *  - rem ↔ px conversion at the canonical 16px root.
 *  - Editing the rem input writes a `Nrem` string back to onChange.
 *  - The raw text input still accepts a `clamp(...)` value.
 *  - The preview bar's width scales relative to the viewport.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import {
  LayoutGridEditor,
  parsePx,
  parseRem,
  pxToRem,
  remToPx,
} from './LayoutGridEditor';

describe('LayoutGridEditor — conversion helpers', () => {
  it('parses rem and px values correctly', () => {
    expect(parseRem('48rem')).toBe(48);
    expect(parseRem('1.5rem')).toBe(1.5);
    expect(parseRem('720px')).toBeNull();
    expect(parsePx('720px')).toBe(720);
    expect(parsePx('48rem')).toBeNull();
  });

  it('round-trips rem ↔ px at the 16px root', () => {
    expect(remToPx(1)).toBe(16);
    expect(remToPx(45)).toBe(720);
    expect(pxToRem(720)).toBe(45);
    expect(pxToRem(16)).toBe(1);
    // 1.5rem ↔ 24px — common in spacing scales.
    expect(remToPx(1.5)).toBe(24);
    expect(pxToRem(24)).toBe(1.5);
  });
});

describe('LayoutGridEditor — UI', () => {
  it('renders both content + wide rows', () => {
    render(
      <LayoutGridEditor
        layout={{ contentSize: '45rem', wideSize: '74rem' }}
        onChange={vi.fn()}
      />,
    );
    expect(screen.getByTestId('layout-content-rem')).toBeInTheDocument();
    expect(screen.getByTestId('layout-wide-rem')).toBeInTheDocument();
  });

  it('writes a rem string when the rem input changes', () => {
    const onChange = vi.fn();
    render(
      <LayoutGridEditor
        layout={{ contentSize: '45rem', wideSize: '74rem' }}
        onChange={onChange}
      />,
    );
    fireEvent.change(screen.getByTestId('layout-content-rem'), {
      target: { value: '50' },
    });
    expect(onChange).toHaveBeenCalled();
    expect(onChange.mock.calls[0][0]).toEqual({
      contentSize: '50rem',
      wideSize: '74rem',
    });
  });

  it('px readout is derived from the rem value', () => {
    render(
      <LayoutGridEditor
        layout={{ contentSize: '45rem', wideSize: '74rem' }}
        onChange={vi.fn()}
      />,
    );
    const pxInput = screen.getByTestId('layout-content-px') as HTMLInputElement;
    expect(pxInput.value).toBe('720');
  });

  it('accepts a clamp() value via the raw text input', () => {
    const onChange = vi.fn();
    render(
      <LayoutGridEditor
        layout={{ contentSize: '45rem', wideSize: '74rem' }}
        onChange={onChange}
      />,
    );
    fireEvent.change(screen.getByTestId('layout-content-raw'), {
      target: { value: 'clamp(20rem, 50vw, 60rem)' },
    });
    expect(onChange.mock.calls[0][0].contentSize).toBe('clamp(20rem, 50vw, 60rem)');
  });

  it('preview bar inline width is set as a percentage of the viewport', () => {
    render(
      <LayoutGridEditor
        layout={{ contentSize: '45rem', wideSize: '74rem' }}
        onChange={vi.fn()}
      />,
    );
    const swatch = screen.getByTestId('token-preview-swatch-content') as HTMLDivElement;
    // The bar uses width % (relative to the viewport) — we only assert
    // that *some* percentage width landed, not the exact value, since
    // jsdom's innerWidth defaults vary.
    expect(swatch.style.width).toMatch(/%$/);
  });
});
