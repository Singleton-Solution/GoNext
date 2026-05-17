/**
 * Shared test setup for GoNext React tests.
 *
 * Wire this in from a per-app `vitest.setup.ts`:
 *
 *   import '@gonext/test-config/setup';
 *
 * Responsibilities:
 *  1. Register `@testing-library/jest-dom` custom matchers
 *     (`toBeInTheDocument`, `toHaveTextContent`, etc.) on Vitest's `expect`.
 *  2. Provide a default `fetch` stub so accidental un-mocked network calls
 *     fail loudly with a clear message rather than hanging or hitting the
 *     real network. Tests that need real-looking responses should use MSW —
 *     see `docs/11-testing-ci.md` §4.1.
 *  3. Run React Testing Library's `cleanup()` after every test to unmount
 *     components and reset the DOM. Vitest does NOT do this automatically
 *     when `globals: true`; we have to opt in explicitly.
 */
import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

afterEach(() => {
  cleanup();
});

// Default fetch stub. Any test that wants real HTTP shape should override
// this with MSW (`server.use(http.get(...))`) or its own `vi.spyOn(global, 'fetch')`.
// Failing loudly is intentional — silently returning `undefined` would let
// bugs hide behind optional chaining.
if (typeof globalThis.fetch === 'undefined' || !('mock' in (globalThis.fetch as object))) {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString();
    throw new Error(
      `[@gonext/test-config] Unmocked fetch call to "${url}". ` +
        `Use MSW (see docs/11-testing-ci.md §4.1) or override globalThis.fetch in this test.`,
    );
  }) as unknown as typeof fetch;
}
