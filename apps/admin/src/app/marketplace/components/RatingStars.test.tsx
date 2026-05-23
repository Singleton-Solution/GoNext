/**
 * RatingStars tests.
 *
 * Verifies:
 *   - read-only mode renders 5 star glyphs reflecting the value and
 *     attaches the right aria-label,
 *   - interactive mode renders a radiogroup of 5 buttons that fires
 *     onChange with the picked integer.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { RatingStars } from './RatingStars';

describe('RatingStars', () => {
  it('renders a read-only display with aria-label reflecting the value', () => {
    render(<RatingStars value={3.7} count={5} />);
    const widget = screen.getByTestId('rating-stars-display');
    expect(widget).toHaveAttribute('aria-label', '3.7 out of 5 stars');
    expect(widget.textContent).toContain('★');
    expect(widget.textContent).toContain('☆');
    expect(widget.textContent).toContain('(5)');
  });

  it('renders an interactive radiogroup that fires onChange', () => {
    const onChange = vi.fn();
    render(<RatingStars value={0} interactive onChange={onChange} />);
    const group = screen.getByTestId('rating-stars-input');
    const buttons = group.querySelectorAll('button[role="radio"]');
    expect(buttons.length).toBe(5);
    fireEvent.click(buttons[3] as Element);
    expect(onChange).toHaveBeenCalledWith(4);
  });
});
