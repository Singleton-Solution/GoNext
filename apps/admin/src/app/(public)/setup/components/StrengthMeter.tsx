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
 */
import type { ReactElement } from 'react';

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

const COLORS: Record<StrengthScore, string> = {
  0: 'transparent',
  1: '#dc2626', // red
  2: '#d97706', // amber
  3: '#65a30d', // lime
  4: '#16a34a', // green
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
      className="setup-strength"
      // aria-live so screen readers announce the change without forcing
      // focus into the meter.
      aria-live="polite"
      id={describedFor ? `${describedFor}-strength` : undefined}
    >
      <div
        className="setup-strength__track"
        role="progressbar"
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuetext={label || 'No password entered'}
      >
        <div
          className="setup-strength__fill"
          style={{
            width: `${pct}%`,
            background: COLORS[score],
          }}
        />
      </div>
      <span className="setup-strength__label muted">{label || 'Enter a password'}</span>
    </div>
  );
}
