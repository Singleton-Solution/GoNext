/**
 * a11y — admin login form.
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * The login form is the first page every admin user lands on; broken
 * a11y here blocks anyone using AT from getting any further. This spec
 * pins:
 *
 *  - Each form field has a programmatic label (`<label for="...">`).
 *  - Inputs declare semantically correct `autocomplete` hints.
 *  - The submit button has an accessible name.
 *  - The page has a top-level `<h1>` and the standard landmark structure.
 *
 * The default scan uses the static fixture from
 * `tools/e2e/tests/a11y/fixtures/markup.ts`, which mirrors
 * `apps/admin/src/app/login/page.tsx`. Set `E2E_A11Y_USE_LIVE=1` to
 * scan the live `/login` URL instead — useful once the admin SPA is
 * wired into a deploy.
 */
import { test, expect } from '@playwright/test';
import { runAxe, formatViolations } from './helpers/axe';
import { loginHtml } from './fixtures/markup';

const useLive = process.env.E2E_A11Y_USE_LIVE === '1';

test.describe('a11y — admin login', () => {
  test('login form has zero WCAG 2.1 AA violations', async ({ page, baseURL }) => {
    if (useLive && baseURL) {
      const loginUrl = `${baseURL.replace(/\/$/, '')}/login`;
      const response = await page.goto(loginUrl, { timeout: 10_000 });
      expect(response, `navigation to ${loginUrl} returned null`).not.toBeNull();
      expect(response!.status()).toBeLessThan(500);
    } else {
      await page.setContent(loginHtml, { waitUntil: 'domcontentloaded' });
    }

    // Sanity-check the fixture / live page is what we think it is before
    // scanning; a missing email field would otherwise fail with a
    // confusing axe message about an unrelated rule.
    await expect(page.getByLabel('Email')).toBeVisible();
    await expect(page.getByLabel('Password')).toBeVisible();

    const results = await runAxe(page);
    expect(
      results.violations,
      formatViolations(results.violations),
    ).toEqual([]);
  });
});
