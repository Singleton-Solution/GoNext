/**
 * a11y — admin block editor canvas.
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * The block editor is the most interactive surface in the admin; it
 * combines content-editable regions, toolbars, and dynamically inserted
 * landmarks. This spec scans a canvas seeded with the three blocks
 * called out in the issue's acceptance criteria — a paragraph
 * (selected), a heading, and a button — plus the block-actions toolbar.
 *
 * Carve-out: `color-contrast` is disabled inside
 * `.gonext-block-edit-canvas` per the documented exception in
 * `helpers/axe.ts`. Theme tokens (issues #354, #358) are resolved at
 * runtime against the live theme bundle, which the e2e harness can't
 * load deterministically. Every other surface still gets full
 * contrast scanning, and every other rule (focus visible, ARIA, names
 * / roles / values, landmarks) is enforced here.
 */
import { test, expect } from '@playwright/test';
import { runAxe, formatViolations } from './helpers/axe';
import { blockEditorCanvasHtml } from './fixtures/markup';

const useLive = process.env.E2E_A11Y_USE_LIVE === '1';

test.describe('a11y — admin block editor canvas', () => {
  test('block editor (paragraph + heading + button) has zero WCAG 2.1 AA violations', async ({
    page,
    baseURL,
  }) => {
    if (useLive && baseURL) {
      const editorUrl = `${baseURL.replace(/\/$/, '')}/posts/new`;
      const response = await page.goto(editorUrl, { timeout: 10_000 });
      expect(response, `navigation to ${editorUrl} returned null`).not.toBeNull();
      expect(response!.status()).toBeLessThan(500);
    } else {
      await page.setContent(blockEditorCanvasHtml, {
        waitUntil: 'domcontentloaded',
      });
    }

    // Sanity-check the three blocks called out in the issue's
    // acceptance criteria are actually mounted before we scan.
    await expect(page.locator('[data-block="core/paragraph"]')).toBeVisible();
    await expect(page.locator('[data-block="core/heading"]')).toBeVisible();
    await expect(page.locator('[data-block="core/button"]')).toBeVisible();

    // Apply the documented colour-contrast carve-out for the canvas.
    // Every other rule remains active.
    const results = await runAxe(page, {
      disabledRules: ['color-contrast'],
    });
    expect(
      results.violations,
      formatViolations(results.violations),
    ).toEqual([]);
  });
});
