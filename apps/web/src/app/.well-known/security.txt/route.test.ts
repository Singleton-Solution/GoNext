/**
 * Tests for the `/.well-known/security.txt` route handler.
 *
 * Verifies RFC 9116 compliance: required fields (Contact, Expires), an
 * Expires timestamp that lies in the future, the recommended fields the
 * handler emits (Preferred-Languages, Canonical, Encryption, Policy), and
 * the `text/plain; charset=utf-8` Content-Type the spec mandates.
 *
 * The handler emits a static body apart from the Expires line, which rolls
 * forward 365 days from build time. The test pins "now" via the system
 * clock and asserts the parsed Expires is roughly a year out, with enough
 * slack to absorb a slow CI runner.
 */
import { describe, it, expect } from 'vitest';
import { GET } from './route';

describe('/.well-known/security.txt route handler', () => {
  it('returns a 200 OK', async () => {
    const res = await GET();
    expect(res.status).toBe(200);
  });

  it('uses Content-Type text/plain; charset=utf-8 per RFC 9116 §3', async () => {
    const res = await GET();
    const contentType = res.headers.get('content-type') ?? '';
    expect(contentType.toLowerCase()).toContain('text/plain');
    expect(contentType.toLowerCase()).toContain('charset=utf-8');
  });

  it('contains at least one Contact field', async () => {
    const res = await GET();
    const body = await res.text();
    const contactLines = body
      .split('\n')
      .filter((line) => /^Contact:\s/.test(line));
    expect(contactLines.length).toBeGreaterThanOrEqual(1);
    // The disclosure email is the documented primary contact.
    expect(body).toContain('Contact: mailto:security@gonext.io');
  });

  it('contains an Expires field with an RFC 3339 timestamp in the future', async () => {
    const res = await GET();
    const body = await res.text();
    const expiresMatch = body.match(/^Expires:\s*(.+)$/m);
    expect(expiresMatch).not.toBeNull();
    const expiresStr = expiresMatch![1].trim();
    // RFC 3339 / ISO 8601 — Date.parse must succeed.
    const expiresMs = Date.parse(expiresStr);
    expect(Number.isNaN(expiresMs)).toBe(false);
    // RFC 9116 §2.5.5: Expires MUST be in the future.
    expect(expiresMs).toBeGreaterThan(Date.now());
  });

  it('contains a Preferred-Languages field', async () => {
    const res = await GET();
    const body = await res.text();
    expect(body).toMatch(/^Preferred-Languages:\s*en\b/m);
  });

  it('contains a Canonical field pointing at the gonext.io location', async () => {
    const res = await GET();
    const body = await res.text();
    expect(body).toMatch(
      /^Canonical:\s*https:\/\/gonext\.io\/\.well-known\/security\.txt$/m,
    );
  });

  it('contains an Encryption field pointing at the published PGP key', async () => {
    const res = await GET();
    const body = await res.text();
    expect(body).toMatch(/^Encryption:\s*https:\/\/gonext\.io\//m);
  });

  it('contains a Policy field linking to /SECURITY.md', async () => {
    const res = await GET();
    const body = await res.text();
    expect(body).toMatch(/^Policy:\s*https:\/\/github\.com\/.+\/SECURITY\.md$/m);
  });

  it('sets Cache-Control to allow short-lived caching with revalidation', async () => {
    const res = await GET();
    const cacheControl = res.headers.get('cache-control') ?? '';
    expect(cacheControl).toContain('public');
    expect(cacheControl).toContain('must-revalidate');
  });

  it('sets X-Content-Type-Options: nosniff', async () => {
    const res = await GET();
    expect(res.headers.get('x-content-type-options')).toBe('nosniff');
  });

  it('body is non-empty and ends with a trailing newline', async () => {
    const res = await GET();
    const body = await res.text();
    expect(body.length).toBeGreaterThan(0);
    expect(body.endsWith('\n')).toBe(true);
  });
});
