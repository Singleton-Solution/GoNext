/**
 * LoadingState — visual + accessibility contract tests.
 *
 * Loading is a *brand moment* per the handoff — the calm shimmer +
 * emerald spinner is part of the "alive" voice. We pin:
 *
 *   1. <LoadingState> default — role=status, aria-live=polite,
 *      aria-busy=true, emerald spinner present, label rendered.
 *   2. <LoadingState variant="card"> — renders <SkeletonCard /> as
 *      the surface.
 *   3. <SkeletonRow> widths — full / mid / short / title produce the
 *      right Tailwind width and height shapes.
 *   4. <SkeletonText lines=N> — clamps in the safe range 1..12 and
 *      renders the documented "first full, middle mid, last short"
 *      pattern.
 *   5. <SkeletonCard> — composes title + 3 lines + tile in a paper-2
 *      card, exposes its srLabel to assistive tech.
 *
 * We assert on data-testid + className fragments since jsdom does
 * not paint Tailwind, but we *do* assert that the gn-shimmer /
 * gn-spin animation utility names are present so a tokens-rename
 * elsewhere can't silently kill the motion.
 */
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import {
  LoadingState,
  SkeletonCard,
  SkeletonRow,
  SkeletonText,
} from './LoadingState';

describe('<LoadingState>', () => {
  it('renders the spinner variant with role=status and an emerald ring', () => {
    render(<LoadingState label="Fetching post · 142 of 142" />);

    const root = screen.getByTestId('loading-state');
    expect(root.getAttribute('role')).toBe('status');
    expect(root.getAttribute('aria-live')).toBe('polite');
    expect(root.getAttribute('aria-busy')).toBe('true');
    expect(root.getAttribute('data-variant')).toBe('spinner');

    // Spinner is a 14px ring with a paper-3 base + emerald top edge,
    // animated via the gn-spin keyframe.
    const spinner = screen.getByTestId('loading-spinner');
    expect(spinner.className).toContain('h-[14px]');
    expect(spinner.className).toContain('w-[14px]');
    expect(spinner.className).toContain('border-paper-3');
    expect(spinner.className).toContain('border-t-emerald');
    expect(spinner.className).toContain('animate-[gn-spin_0.8s_linear_infinite]');

    // Label is visible to sighted users.
    const label = screen.getByTestId('loading-state-label');
    expect(label.textContent).toBe('Fetching post · 142 of 142');
  });

  it('emits an sr-only "Loading" label when no label prop is given', () => {
    render(<LoadingState />);
    // Even silent spinners need a screenreader handle.
    const root = screen.getByTestId('loading-state');
    expect(root.querySelector('.sr-only')?.textContent).toBe('Loading');
    // And no visible label.
    expect(screen.queryByTestId('loading-state-label')).not.toBeInTheDocument();
  });

  it('renders the card variant via <SkeletonCard>', () => {
    render(<LoadingState variant="card" label="Fetching dashboard" />);
    const root = screen.getByTestId('loading-state');
    expect(root.getAttribute('data-variant')).toBe('card');
    // SkeletonCard composes inside.
    const card = within(root).getByTestId('skeleton-card');
    expect(card).toBeInTheDocument();
    // The screenreader label rides through to SkeletonCard.srLabel.
    expect(card.querySelector('.sr-only')?.textContent).toBe(
      'Fetching dashboard',
    );
  });
});

describe('<SkeletonRow>', () => {
  it('defaults to a full-width 12px shimmer bar', () => {
    render(<SkeletonRow />);
    const row = screen.getByTestId('skeleton-row');
    expect(row.getAttribute('data-width')).toBe('full');
    expect(row.getAttribute('aria-hidden')).toBe('true');
    expect(row.className).toContain('h-3');
    expect(row.className).toContain('w-full');
    expect(row.className).toContain(
      'animate-[gn-shimmer_1.6s_linear_infinite]',
    );
  });

  it('renders the mid, short, and title width variants', () => {
    const { rerender } = render(<SkeletonRow width="mid" />);
    expect(screen.getByTestId('skeleton-row').className).toContain('w-[78%]');

    rerender(<SkeletonRow width="short" />);
    expect(screen.getByTestId('skeleton-row').className).toContain('w-[44%]');

    rerender(<SkeletonRow width="title" />);
    const title = screen.getByTestId('skeleton-row');
    expect(title.className).toContain('w-[60%]');
    // Title is taller (h-9 / 36px) and uses rounded-md.
    expect(title.className).toContain('h-9');
    expect(title.className).toContain('rounded-md');
  });
});

describe('<SkeletonText>', () => {
  it('defaults to 3 lines with the full / mid / short pattern', () => {
    render(<SkeletonText />);
    const wrap = screen.getByTestId('skeleton-text');
    const rows = within(wrap).getAllByTestId('skeleton-row');
    expect(rows).toHaveLength(3);
    expect(rows[0]?.getAttribute('data-width')).toBe('full');
    expect(rows[1]?.getAttribute('data-width')).toBe('mid');
    expect(rows[2]?.getAttribute('data-width')).toBe('short');
  });

  it('renders the requested line count, clamping into the safe range', () => {
    const { rerender } = render(<SkeletonText lines={5} />);
    expect(
      within(screen.getByTestId('skeleton-text')).getAllByTestId('skeleton-row'),
    ).toHaveLength(5);

    rerender(<SkeletonText lines={1} />);
    const oneRow = within(screen.getByTestId('skeleton-text')).getAllByTestId(
      'skeleton-row',
    );
    expect(oneRow).toHaveLength(1);
    // Single-line variant uses the short width so it doesn't read as
    // a wall of placeholder.
    expect(oneRow[0]?.getAttribute('data-width')).toBe('short');

    rerender(<SkeletonText lines={1000} />);
    // Upper clamp at 12.
    expect(
      within(screen.getByTestId('skeleton-text')).getAllByTestId('skeleton-row'),
    ).toHaveLength(12);

    rerender(<SkeletonText lines={0} />);
    // Lower clamp at 1.
    expect(
      within(screen.getByTestId('skeleton-text')).getAllByTestId('skeleton-row'),
    ).toHaveLength(1);
  });
});

describe('<SkeletonCard>', () => {
  it('renders a paper-2 card with title + 3 lines + a tile', () => {
    render(<SkeletonCard />);
    const card = screen.getByTestId('skeleton-card');
    expect(card.className).toContain('bg-paper-2');
    expect(card.className).toContain('rounded-lg');
    expect(card.className).toContain('border-border');
    // Status + label so AT users hear "Loading".
    expect(card.getAttribute('role')).toBe('status');
    expect(card.getAttribute('aria-busy')).toBe('true');
    expect(card.querySelector('.sr-only')?.textContent).toBe('Loading');

    // Five rows: 1 title + 3 body lines + 1 tile.
    const rows = within(card).getAllByTestId('skeleton-row');
    expect(rows).toHaveLength(4); // title + full + mid + short
    expect(within(card).getByTestId('skeleton-card-tile')).toBeInTheDocument();
  });

  it('forwards the srLabel for assistive tech', () => {
    render(<SkeletonCard srLabel="Fetching dashboard widgets" />);
    expect(
      screen.getByTestId('skeleton-card').querySelector('.sr-only')?.textContent,
    ).toBe('Fetching dashboard widgets');
  });
});
