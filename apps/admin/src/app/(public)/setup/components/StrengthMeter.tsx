'use client';

/**
 * StrengthMeter — visualizes password strength as a progress bar.
 *
 * Scoring is intentionally simple (length + character class diversity).
 * The server is the actual gate (≥12 characters, see
 * apps/api/internal/setup/deps.go MinPasswordLength); the meter is a
 * UX nudge, not a security boundary. We resist the temptation to ship
 * zxcvbn here — its dictionary alone is ~700 KiB minified, which is
 * untenable for a first-run page that should load instantly.
 *
 * The strength scale:
 *
 *   - 0 (empty)    — no input yet; bar hidden
 *   - 1 (weak)     — under the server's 12-char floor
 *   - 2 (fair)     — meets the floor but only one or two char classes
 *   - 3 (good)     — three+ char classes OR ≥16 chars
 *   - 4 (strong)   — four char classes AND ≥16 chars
 *
 * Char classes counted: lowercase, uppercase, digit, symbol.
 *
 * Visual treatment matches the Living-Systems brand: the fill is a
 * linear gradient that starts emerald and rolls into lavender as the
 * score climbs — the same emerald → lavender pairing the rest of the
 * data-viz layer uses. The track itself is a sunken paper-3 well so
 * the bar reads as a fluid level, not a hard rule.
 */
import type { ReactElement } from 'react';

import { cn } from '@/lib/utils';

export type StrengthScore = 0 | 1 | 2 | 3 | 4;

export interface StrengthMeterProps {
  /** The current password value. */
  password: string;
  /** Optional id of the input the meter describes (for aria-describedby). */
  describedFor?: string;
}

/**
 * Computes a 0–4 strength score for the given password. Exported so the
 * wizard can gate the "Next" button on the same scale the meter shows.
 */
export function scorePassword(password: string): StrengthScore {
  if (password.length === 0) return 0;
  // Server floor is 12; anything below is automatically "weak".
  if (password.length < 12) return 1;

  let classes = 0;
  if (/[a-z]/.test(password)) classes += 1;
  if (/[A-Z]/.test(password)) classes += 1;
  if (/\d/.test(password)) classes += 1;
  if (/[^A-Za-z0-9]/.test(password)) classes += 1;

  if (password.length >= 16 && classes >= 4) return 4;
  if (password.length >= 16 || classes >= 3) return 3;
  return 2;
}

const LABELS: Record<StrengthScore, string> = {
  0: '',
  1: 'Too short',
  2: 'Fair',
  3: 'Good',
  4: 'Strong',
};

/**
 * As the score climbs the bar transitions from solid emerald (1) into
 * a richer emerald → lavender gradient (4). The track stays paper-3 so
 * the fill reads against a sunken cream surface.
 *
 * Token mapping:
 *   1 — solid emerald-deep (danger-low, but still on-brand cream-side)
 *   2 — emerald → emerald-bright (warm, "growing")
 *   3 — emerald-bright → lavender (the crossover)
 *   4 — emerald-bright → lavender-deep (full traversal)
 */
const FILL_GRADIENTS: Record<StrengthScore, string> = {
  0: 'transparent',
  1: 'linear-gradient(90deg, #DC2626 0%, #DC2626 100%)',
  2: 'linear-gradient(90deg, #10B981 0%, #34D399 100%)',
  3: 'linear-gradient(90deg, #34D399 0%, #A78BFA 100%)',
  4: 'linear-gradient(90deg, #34D399 0%, #7C3AED 100%)',
};

/**
 * Renders the strength meter. The bar grows in 25% steps; the label
 * names the score so a screen reader announces the same information a
 * sighted user reads.
 */
export function StrengthMeter({
  password,
  describedFor,
}: StrengthMeterProps): ReactElement {
  const score = scorePassword(password);
  const pct = score * 25;
  const label = LABELS[score];
  return (
    <div
      className={cn(
        'setup-strength',
        'mt-2 flex flex-col gap-2',
      )}
      // aria-live so screen readers announce the change without forcing
      // focus into the meter.
      aria-live="polite"
      id={describedFor ? `${describedFor}-strength` : undefined}
    >
      <div
        className={cn(
          'setup-strength__track',
          'h-[6px] w-full overflow-hidden rounded-pill bg-paper-3 border border-border-subtle',
        )}
        role="progressbar"
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuetext={label || 'No password entered'}
      >
        <div
          className={cn(
            'setup-strength__fill',
            'h-full rounded-pill transition-[width,background] duration-[260ms] ease-brand',
          )}
          style={{
            width: `${pct}%`,
            background: FILL_GRADIENTS[score],
          }}
        />
      </div>
      <span
        className={cn(
          'setup-strength__label',
          'font-sans text-xs text-fg-subtle',
        )}
      >
        {label || 'Enter a password'}
      </span>
    </div>
  );
}
