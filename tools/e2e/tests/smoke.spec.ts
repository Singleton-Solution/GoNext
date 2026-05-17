/**
 * Smoke test: the most basic check that the stack is alive.
 *
 * Fetches the home page and asserts a 200 response. This is the
 * scaffold; richer journeys (login, dashboard, etc.) land in
 * follow-up issues per docs/11-testing-ci.md §11.
 */

import { test, expect } from '../fixtures/server';

test.describe('smoke', () => {
  test('home page responds 200', async ({ serverRequest, baseURL }) => {
    const response = await serverRequest.get(baseURL);
    expect(
      response.status(),
      `GET ${baseURL} should return 200, got ${response.status()}`,
    ).toBe(200);
  });

  test('home page renders in the browser', async ({ page, baseURL }) => {
    const response = await page.goto(baseURL!);
    expect(response, `navigation to ${baseURL} returned null`).not.toBeNull();
    expect(response!.status()).toBe(200);
    // The body should at least be parseable; specific selectors land with
    // the real web app (see apps/web).
    await expect(page.locator('body')).toBeVisible();
  });
});
