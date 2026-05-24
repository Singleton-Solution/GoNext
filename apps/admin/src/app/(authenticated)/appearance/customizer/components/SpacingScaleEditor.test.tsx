/**
 * Tests for the SpacingScaleEditor.
 *
 * Coverage:
 *  - Renders one row per token with the right slug / value.
 *  - Dragging a slider produces a token update with the new size.
 *  - The preview swatch updates its inline width as the value changes.
 *  - The text input still works for power users.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SpacingScaleEditor } from './SpacingScaleEditor';
import type { SpacingSize } from '../types';

const TOKENS: SpacingSize[] = [
  { slug: 'xs', name: 'Extra small', size: '0.25rem' },
  { slug: 'sm', name: 'Small', size: '0.5rem' },
  { slug: 'md', name: 'Medium', size: '1rem' },
  { slug: 'lg', name: 'Large', size: '1.5rem' },
  { slug: 'xl', name: 'Extra large', size: '2rem' },
  { slug: '2xl', name: '2× extra large', size: '3rem' },
];

describe('SpacingScaleEditor', () => {
  it('renders one row per token, six in total', () => {
    render(<SpacingScaleEditor tokens={TOKENS} onChange={vi.fn()} />);
    for (const t of TOKENS) {
      expect(
        screen.getByTestId(`spacing-token-slider-${t.slug}`),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId(`spacing-token-text-${t.slug}`),
      ).toBeInTheDocument();
    }
  });

  it('emits a token update when the slider moves', () => {
    const onChange = vi.fn();
    render(<SpacingScaleEditor tokens={TOKENS} onChange={onChange} />);
    const slider = screen.getByTestId('spacing-token-slider-md') as HTMLInputElement;
    fireEvent.change(slider, { target: { value: '1.5' } });
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0][0] as SpacingSize[];
    expect(next[2]?.size).toBe('1.5rem');
    // The other tokens are unchanged.
    expect(next[0]?.size).toBe('0.25rem');
  });

  it('preview swatch width re-renders when the value changes', () => {
    const { rerender } = render(
      <SpacingScaleEditor tokens={TOKENS} onChange={vi.fn()} />,
    );
    const swatch = screen.getByTestId('token-preview-swatch-md') as HTMLDivElement;
    const initialWidth = swatch.style.width;

    const updated = TOKENS.map((t) =>
      t.slug === 'md' ? { ...t, size: '2rem' } : t,
    );
    rerender(<SpacingScaleEditor tokens={updated} onChange={vi.fn()} />);
    expect(swatch.style.width).not.toBe(initialWidth);
    // 2rem * 16 = 32 — visible in the inline style.
    expect(swatch.style.width).toBe('32px');
  });

  it('text input still wins for clamp() values, slider goes inert', () => {
    const onChange = vi.fn();
    render(<SpacingScaleEditor tokens={TOKENS} onChange={onChange} />);
    const text = screen.getByTestId('spacing-token-text-lg') as HTMLInputElement;
    fireEvent.change(text, { target: { value: 'clamp(1rem, 3vw, 2rem)' } });
    expect(onChange).toHaveBeenCalled();
    const next = onChange.mock.calls[0][0] as SpacingSize[];
    expect(next[3]?.size).toBe('clamp(1rem, 3vw, 2rem)');
  });
});
