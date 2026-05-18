/**
 * a11y — admin post list (`<ResourceList>`).
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * `<ResourceList>` is the shared shell for every CRUD list in the admin
 * (posts, pages, users, comments, media). One a11y bug here multiplies
 * across the product, so the scan is treated as a release blocker.
 *
 * What this spec pins:
 *
 *  - Toolbar / search / filter chip have accessible names.
 *  - The table is labelled (caption or aria-labelledby), uses `<th
 *    scope="col">`, and exposes `aria-sort` on sortable columns.
 *  - Each row checkbox has an `aria-label`.
 *  - Pagination buttons are reachable and labelled.
 *
 * The fixture in `fixtures/markup.ts` mirrors the rendered shape of
 * `apps/admin/src/components/ResourceList/ResourceList.tsx`. Set
 * `E2E_A11Y_USE_LIVE=1` to scan the live `/posts` URL instead.
 */
import { test, expect } from '@playwright/test';
import { runAxe, formatViolations } from './helpers/axe';
import { postListHtml } from './fixtures/markup';

const useLive = process.env.E2E_A11Y_USE_LIVE === '1';

test.describe('a11y — admin post list', () => {
  test('post list has zero WCAG 2.1 AA violations', async ({ page, baseURL }) => {
    if (useLive && baseURL) {
      const postsUrl = `${baseURL.replace(/\/$/, '')}/posts`;
      const response = await page.goto(postsUrl, { timeout: 10_000 });
      expect(response, `navigation to ${postsUrl} returned null`).not.toBeNull();
      expect(response!.status()).toBeLessThan(500);
    } else {
      await page.setContent(postListHtml, { waitUntil: 'domcontentloaded' });
    }

    // Spot-check the fixture / live page is rendered before scanning.
    await expect(page.getByRole('heading', { name: 'Posts' })).toBeVisible();
    await expect(page.getByLabel('Search')).toBeVisible();

    const results = await runAxe(page);
    expect(
      results.violations,
      formatViolations(results.violations),
    ).toEqual([]);
  });
});
