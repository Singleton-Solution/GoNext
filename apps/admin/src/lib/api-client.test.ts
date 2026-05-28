/**
 * Tests for the admin API base URL resolver.
 *
 * Regression coverage for issue #498: when `apiBaseUrl` was a
 * module-load constant, Next.js's Client Component bundler evaluated
 * `typeof window === 'undefined'` at build time (Node process, no
 * window) and baked the docker-internal `http://api:8080` into the
 * browser bundle, breaking every client fetch with DNS failures.
 *
 * The fix exposes `apiBaseUrl` as a function so the runtime branch
 * resolves in the actual execution environment. These tests pin that
 * behavior:
 *
 *   - With no `window`, the server branch picks `GONEXT_API_URL`.
 *   - With a `window`, the client branch picks `NEXT_PUBLIC_API_URL`.
 *   - Empty string survives as a valid "same-origin via rewrites"
 *     value — it must NOT be coerced to the localhost default.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('apiBaseUrl', () => {
  const originalEnv = { ...process.env };

  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    process.env = { ...originalEnv };
    vi.unstubAllGlobals();
  });

  it('returns GONEXT_API_URL when running server-side (no window)', async () => {
    process.env.GONEXT_API_URL = 'http://api:8080';
    process.env.NEXT_PUBLIC_API_URL = '';
    vi.stubGlobal('window', undefined);
    const { apiBaseUrl } = await import('./api-client');
    expect(apiBaseUrl()).toBe('http://api:8080');
  });

  it('returns NEXT_PUBLIC_API_URL when running in browser (window defined)', async () => {
    process.env.GONEXT_API_URL = 'http://api:8080';
    process.env.NEXT_PUBLIC_API_URL = '';
    vi.stubGlobal('window', {} as unknown as Window);
    const { apiBaseUrl } = await import('./api-client');
    expect(apiBaseUrl()).toBe('');
  });

  it('preserves empty string as a valid value (no falsy coerce)', async () => {
    process.env.NEXT_PUBLIC_API_URL = '';
    vi.stubGlobal('window', {} as unknown as Window);
    const { apiBaseUrl } = await import('./api-client');
    expect(apiBaseUrl()).toBe('');
  });
});
