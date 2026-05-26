'use client';

/**
 * RatingStars — read-only display and interactive input.
 *
 * Two render modes selected by the `interactive` prop:
 *   - false (default): a non-interactive 1..5 strip whose filled slots
 *     reflect the current `value` (rounded down to the nearest integer
 *     for the visual; the precise average is available via the
 *     `aria-label`).
 *   - true: keyboard- and mouse-accessible radiogroup; each slot fires
 *     `onChange` with the chosen integer.
 *
 * Brand
 * =====
 * Stars are `--emerald-deep` on cream — the "alive" green that runs
 * across the marketplace surface. Empty slots stay at `--fg-faint` so
 * the relative fill is legible at a glance. The trailing rating count
 * is rendered in monospace so the per-card foot row aligns vertically
 * across the grid.
 *
 * Why a single component for both modes: the visual treatment is
 * identical and downstream code wants to use the same "stars look"
 * for the listing grid card AND the rating-submission form. Splitting
 * would duplicate the styling without adding clarity.
 *
 * We use unicode stars (★ / ☆) rather than SVG so the component has
 * zero asset dependencies and renders crisp at any size; the
 * accessibility name still spells "5 stars" so screen readers don't
 * announce the glyph.
 */

import type { CSSProperties, ReactElement } from 'react';

const styles: Record<string, CSSProperties> = {
  group: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 2,
    color: 'var(--emerald-deep)',
    fontSize: 14,
    lineHeight: 1,
  },
  star: {
    display: 'inline-block',
    width: '1.1em',
    textAlign: 'center',
  },
  starEmpty: {
    color: 'var(--fg-faint)',
  },
  button: {
    background: 'transparent',
    border: 0,
    cursor: 'pointer',
    color: 'inherit',
    font: 'inherit',
    padding: 0,
    width: '1.4em',
    height: '1.4em',
  },
  count: {
    marginLeft: 6,
    fontFamily: 'var(--font-mono)',
    color: 'var(--fg-subtle)',
    fontSize: 'var(--t-xs)',
  },
};

export interface RatingStarsProps {
  /** Current value, 0..5 (fractional accepted in display mode). */
  value: number;
  /** Total number of ratings — shown beside the strip in display mode. */
  count?: number;
  /** When true, render as an input radiogroup. Default false. */
  interactive?: boolean;
  /** Called with the new integer star value in interactive mode. */
  onChange?: (next: number) => void;
  /** Optional label override; defaults to "<n> out of 5 stars". */
  ariaLabel?: string;
}

export function RatingStars({
  value,
  count,
  interactive = false,
  onChange,
  ariaLabel,
}: RatingStarsProps): ReactElement {
  const rounded = Math.max(0, Math.min(5, Math.round(value)));
  const display = Math.max(0, Math.min(5, value));
  const label = ariaLabel ?? `${display.toFixed(1)} out of 5 stars`;

  if (!interactive) {
    return (
      <span
        style={styles.group}
        role="img"
        aria-label={label}
        data-testid="rating-stars-display"
      >
        {[1, 2, 3, 4, 5].map((n) => (
          <span
            key={n}
            aria-hidden="true"
            style={n <= rounded ? styles.star : { ...styles.star, ...styles.starEmpty }}
          >
            {n <= rounded ? '★' : '☆'}
          </span>
        ))}
        {typeof count === 'number' ? (
          <span style={styles.count} aria-hidden="true">
            ({count})
          </span>
        ) : null}
      </span>
    );
  }

  return (
    <span
      style={styles.group}
      role="radiogroup"
      aria-label={ariaLabel ?? 'Rate this plugin'}
      data-testid="rating-stars-input"
    >
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          role="radio"
          aria-checked={n === rounded}
          aria-label={`${n} star${n === 1 ? '' : 's'}`}
          style={
            n <= rounded
              ? styles.button
              : { ...styles.button, color: 'var(--fg-faint)' }
          }
          onClick={() => onChange?.(n)}
        >
          {n <= rounded ? '★' : '☆'}
        </button>
      ))}
    </span>
  );
}
