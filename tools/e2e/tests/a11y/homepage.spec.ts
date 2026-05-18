/**
 * a11y — gn-hello homepage.
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * This spec renders the gn-hello front page (header, latest-posts main,
 * footer) and asserts axe-core finds zero violations against the
 * standard WCAG 2.1 AA tag set.
 *
 * Scan source: the a11y suite is designed to give a deterministic
 * verdict every CI run — there's no "skip because the stack is down"
 * mode for an a11y gate. By default we render the static fixture in
 * `tools/e2e/tests/a11y/fixtures/markup.ts`, which mirrors the
 * rendered theme markup. Opt into scanning the live stack by setting
 * `E2E_A11Y_USE_LIVE=1` in the environment — useful for smoke-testing
 * a real deploy once the front-end is wired up.
 */
import { test, expect } from '@playwright/test';
import { runAxe, formatViolations } from './helpers/axe';
import { homepageHtml } from './fixtures/markup';

const useLive = process.env.E2E_A11Y_USE_LIVE === '1';

test.describe('a11y — homepage', () => {
  test('homepage has zero WCAG 2.1 AA violations', async ({ page, baseURL }) => {
    if (useLive && baseURL) {
      const response = await page.goto(baseURL, { timeout: 10_000 });
      expect(response, `navigation to ${baseURL} returned null`).not.toBeNull();
      expect(response!.status()).toBeLessThan(500);
    } else {
      await page.setContent(homepageHtml, { waitUntil: 'domcontentloaded' });
    }

    const results = await runAxe(page);
    expect(
      results.violations,
      formatViolations(results.violations),
    ).toEqual([]);
  });
});
