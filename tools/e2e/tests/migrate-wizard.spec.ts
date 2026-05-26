/**
 * Happy-path Playwright walk through the migration wizard.
 *
 * Loads /migrate in the admin app and clicks through all five steps.
 * The wizard's API endpoints are stubbed at the route level so the
 * test doesn't need a running importer — the wizard's synthetic
 * fallback covers the rest.
 *
 * This spec is skipped when the admin app isn't reachable. The
 * server fixture handles that uniformly across the e2e suite.
 *
 * Issue #234.
 */

import { test, expect } from '../fixtures/server';

const MIGRATE_PATH = '/migrate';

test.describe('migration wizard', () => {
  test('walks all five steps to the report', async ({ page, baseURL }) => {
    // Stub the wizard's three API calls so the test runs without a
    // live importer. Each route returns canned data shaped like the
    // real responses.
    await page.route('**/api/v1/admin/migrate/dry-run', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          authors: 1,
          categories: 1,
          tags: 0,
          posts: 5,
          attachments: 2,
          comments: 3,
          warnings: ['e2e-stubbed dry-run'],
        }),
      });
    });
    await page.route('**/api/v1/admin/migrate/start', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ jobId: 'e2e-job' }),
      });
    });
    // The status endpoint returns done immediately so the test
    // doesn't need to wait on a polling loop.
    await page.route('**/api/v1/admin/migrate/status**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          jobId: 'e2e-job',
          status: 'done',
          percent: 100,
          phase: 'done',
          counts: {
            authors: 1,
            categories: 1,
            tags: 0,
            posts: 5,
            attachments: 2,
            comments: 3,
            warnings: [],
          },
          errors: [],
        }),
      });
    });

    await page.goto(`${baseURL}${MIGRATE_PATH}`);
    // Step 1: pick WXR (default) and upload a tiny file.
    await expect(page.getByText('1. Source')).toBeVisible();
    const buffer = Buffer.from('<rss />');
    await page.locator('[data-testid=source-wxr-file]').setInputFiles({
      name: 'tiny.xml',
      mimeType: 'application/xml',
      buffer,
    });
    await page.locator('[data-testid=source-next]').click();
    // Step 2.
    await expect(page.getByText('2. Options')).toBeVisible();
    await page.locator('[data-testid=options-next]').click();
    // Step 3 — auto-fetches the preview.
    await expect(page.locator('[data-testid=preview-counts]')).toBeVisible();
    await page.locator('[data-testid=preview-next]').click();
    // Step 4 — run, polling resolves to done immediately.
    await expect(page.locator('[data-testid=run-progressbar]')).toBeVisible();
    await expect(page.locator('[data-testid=run-next]')).toBeEnabled({ timeout: 10_000 });
    await page.locator('[data-testid=run-next]').click();
    // Step 5 — report.
    await expect(page.getByText('5. Report')).toBeVisible();
    await expect(page.locator('[data-testid=report-counts]')).toBeVisible();
  });
});
