/**
 * Tests for the SRI helpers.
 *
 * The hashes asserted here come from running OpenSSL on the same byte
 * sequences out-of-band:
 *
 *   echo -n "plugin-bytes" | openssl dgst -sha384 -binary | base64
 *
 * Hardcoding the expected digest is the only reliable way to verify
 * the encoder is correct — re-hashing inside the test would only prove
 * that the function returns whatever it just computed.
 */
import { describe, expect, it } from 'vitest';
import {
  SRI_ALGORITHMS,
  computeSRI,
  isValidSRIAttribute,
  parseSRI,
  verifySRI,
} from './sri';

const SAMPLE_INPUT = 'plugin-bytes';

// Computed via `printf 'plugin-bytes' | openssl dgst -sha{256,384,512} -binary | base64`
const SAMPLE_HASHES = {
  sha256: 'sha256-0713HW3vdVfOYbSEhb0BIZ+a+KYiutja/xfx1H9+wNc=',
  sha384:
    'sha384-AxDMIqo9dfV5IUTjnquaoPQ0ZJmXM5LaPiNZXfVTrejh97+msKLNSHd7SpJtmBkv',
  sha512:
    'sha512-ku0IjooVqHA6DF4U0smP5C1bI+fBz61WhiSsTwCjCFv7o08g+qonLSrOXo2R2DMfEAmIn6Nrp3LXfs5J2WnCZg==',
} as const;

describe('SRI_ALGORITHMS', () => {
  it('declares all three spec-allowed algorithms', () => {
    expect(SRI_ALGORITHMS).toEqual(['sha384', 'sha512', 'sha256']);
  });
});

describe('computeSRI', () => {
  it('produces the spec-shaped attribute by default (sha384)', async () => {
    const got = await computeSRI(SAMPLE_INPUT);
    expect(got.algorithm).toBe('sha384');
    expect(got.attribute).toBe(SAMPLE_HASHES.sha384);
  });

  it('honors the explicit algorithm choice (sha256)', async () => {
    const got = await computeSRI(SAMPLE_INPUT, 'sha256');
    expect(got.attribute).toBe(SAMPLE_HASHES.sha256);
  });

  it('honors the explicit algorithm choice (sha512)', async () => {
    const got = await computeSRI(SAMPLE_INPUT, 'sha512');
    expect(got.attribute).toBe(SAMPLE_HASHES.sha512);
  });

  it('accepts Uint8Array input identically to string input', async () => {
    const bytes = new TextEncoder().encode(SAMPLE_INPUT);
    const fromBytes = await computeSRI(bytes);
    const fromString = await computeSRI(SAMPLE_INPUT);
    expect(fromBytes.attribute).toBe(fromString.attribute);
  });

  it('accepts ArrayBuffer input identically to Uint8Array input', async () => {
    const u8 = new TextEncoder().encode(SAMPLE_INPUT);
    const ab = u8.buffer.slice(u8.byteOffset, u8.byteOffset + u8.byteLength);
    const fromAB = await computeSRI(ab);
    const fromU8 = await computeSRI(u8);
    expect(fromAB.attribute).toBe(fromU8.attribute);
  });

  it('rejects unsupported algorithms', async () => {
    await expect(
      computeSRI('x', 'md5' as unknown as 'sha256'),
    ).rejects.toThrow(/unsupported SRI algorithm/);
  });
});

describe('parseSRI', () => {
  it('parses a single sha384 attribute', () => {
    const got = parseSRI(SAMPLE_HASHES.sha384);
    expect(got.algorithm).toBe('sha384');
    expect(got.digestBase64.length).toBeGreaterThan(0);
  });

  it('picks the first usable token in a comma-separated list', () => {
    const compound = `${SAMPLE_HASHES.sha384}, ${SAMPLE_HASHES.sha512}`;
    const got = parseSRI(compound);
    expect(got.algorithm).toBe('sha384');
  });

  it('rejects an unknown algorithm', () => {
    expect(() => parseSRI('md5-AAAA')).toThrowError(/recognized SRI hash/);
  });

  it('rejects an empty digest', () => {
    expect(() => parseSRI('sha384-')).toThrowError(/recognized SRI hash/);
  });

  it('rejects a missing dash separator', () => {
    expect(() => parseSRI('sha384AAAA')).toThrowError(/recognized SRI hash/);
  });

  it('rejects an empty string', () => {
    expect(() => parseSRI('')).toThrow();
  });
});

describe('verifySRI', () => {
  it('returns true on a round-trip with the same input', async () => {
    const ok = await verifySRI(SAMPLE_INPUT, SAMPLE_HASHES.sha384);
    expect(ok).toBe(true);
  });

  it('returns false when bytes do not match', async () => {
    const ok = await verifySRI('different', SAMPLE_HASHES.sha384);
    expect(ok).toBe(false);
  });

  it('accepts a structured SriHash as expected value', async () => {
    const expected = parseSRI(SAMPLE_HASHES.sha384);
    const ok = await verifySRI(SAMPLE_INPUT, expected);
    expect(ok).toBe(true);
  });

  it('uses the algorithm declared in the expected value', async () => {
    const ok = await verifySRI(SAMPLE_INPUT, SAMPLE_HASHES.sha256);
    expect(ok).toBe(true);
  });
});

describe('isValidSRIAttribute', () => {
  it('accepts well-formed attributes', () => {
    expect(isValidSRIAttribute(SAMPLE_HASHES.sha384)).toBe(true);
  });
  it('rejects malformed attributes', () => {
    expect(isValidSRIAttribute('not-an-sri')).toBe(false);
    expect(isValidSRIAttribute('')).toBe(false);
    expect(isValidSRIAttribute('sha999-AAAA')).toBe(false);
  });
});
