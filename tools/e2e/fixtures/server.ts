/**
 * Server fixture: verifies the docker-compose stack is reachable before
 * each test. If the base URL cannot be reached, the test is skipped with
 * a clear, actionable message rather than failing with an opaque socket
 * error.
 *
 * Wire this into specs that need the live stack by importing `test` from
 * this file instead of `@playwright/test` directly:
 *
 *   import { test, expect } from '../fixtures/server';
 *
 * See docs/11-testing-ci.md §11 for the full e2e plan.
 */

import { test as base, expect, type APIRequestContext } from '@playwright/test';

export type ServerFixtures = {
  /**
   * A pre-configured request context pointing at the stack. Useful for
   * API probes from inside a test.
   */
  serverRequest: APIRequestContext;
};

/**
 * Probes the stack with a short timeout. Returns true when a TCP-level
 * response (any HTTP status) comes back, false otherwise. We deliberately
 * do not assert on status here — the stack being *up* is enough; the
 * test itself decides what status is acceptable.
 */
async function isStackReachable(
  request: APIRequestContext,
  baseURL: string,
): Promise<boolean> {
  try {
    const response = await request.get(baseURL, { timeout: 3_000 });
    // Any HTTP response means the stack is up. 5xx might still be valid
    // for some tests; we leave that decision to the spec.
    return response.status() > 0;
  } catch {
    return false;
  }
}

export const test = base.extend<ServerFixtures>({
  serverRequest: async ({ playwright, baseURL }, use, testInfo) => {
    if (!baseURL) {
      throw new Error(
        'baseURL is not configured. Set E2E_BASE_URL or fix playwright.config.ts.',
      );
    }

    const request = await playwright.request.newContext({ baseURL });

    const reachable = await isStackReachable(request, baseURL);
    if (!reachable) {
      testInfo.skip(
        true,
        [
          `docker-compose stack not reachable at ${baseURL}.`,
          'Start it with:  make up   (or: docker compose up -d)',
          'Override the URL with E2E_BASE_URL if it lives elsewhere.',
        ].join('\n'),
      );
    }

    await use(request);
    await request.dispose();
  },
});

export { expect };
