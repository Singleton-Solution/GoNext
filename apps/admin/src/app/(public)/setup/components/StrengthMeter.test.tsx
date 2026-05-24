/**
 * Tests for the StrengthMeter helper. We focus on the pure scoring
 * function — the rendered bar is a thin visualization on top of it.
 */
import { describe, expect, it } from 'vitest';
import { scorePassword } from './StrengthMeter';

describe('scorePassword', () => {
  it('returns 0 for empty input', () => {
    expect(scorePassword('')).toBe(0);
  });

  it('returns 1 (too short) for under 12 characters', () => {
    for (const s of ['x', 'short1!', 'abcdefghij1!'.slice(0, 11)]) {
      expect(scorePassword(s)).toBe(1);
    }
  });

  it('returns 2 (fair) for ≥12 chars with limited class diversity', () => {
    // 12 chars, all lowercase = one char class.
    expect(scorePassword('abcdefghijkl')).toBe(2);
  });

  it('returns 3 (good) for ≥12 chars with three classes', () => {
    expect(scorePassword('abcdefghij1!')).toBe(3);
  });

  it('returns 3 (good) for ≥16 chars even with limited diversity', () => {
    expect(scorePassword('correcthorsebatterystaple')).toBe(3);
  });

  it('returns 4 (strong) for ≥16 chars with four classes', () => {
    expect(scorePassword('Correct1!HorseBatteryStaple')).toBe(4);
  });
});
