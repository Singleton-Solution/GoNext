/**
 * `maskEmail` unit tests — locks down the partial-mask format the list view
 * relies on for PII hygiene (docs/13-security-baseline.md §PII).
 */
import { describe, expect, it } from 'vitest';
import { maskEmail } from './mask';

describe('maskEmail', () => {
  it('keeps the first character of the local part and masks the rest', () => {
    expect(maskEmail('alice@example.com')).toBe('a***@example.com');
  });

  it('works on single-character local parts', () => {
    expect(maskEmail('x@y.io')).toBe('x***@y.io');
  });

  it('preserves the original domain (including dots and subdomains)', () => {
    expect(maskEmail('user@mail.sub.example.co.uk')).toBe(
      'u***@mail.sub.example.co.uk',
    );
  });

  it('returns the input unchanged when there is no @', () => {
    expect(maskEmail('not-an-email')).toBe('not-an-email');
  });

  it('returns the input unchanged when the local part is empty', () => {
    expect(maskEmail('@example.com')).toBe('@example.com');
  });
});
