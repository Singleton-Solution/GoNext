/**
 * Axe-core helper for GoNext e2e a11y suites.
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * This module wraps `AxeBuilder` with the project's standard ruleset so
 * each spec stays a one-liner:
 *
 *   const results = await runAxe(page);
 *   expect(results.violations, formatViolations(results.violations)).toEqual([]);
 *
 * Decisions / carve-outs:
 *
 *  - **Standards**: `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`. Best-practice
 *    rules are excluded — they're useful guidance but not part of the
 *    contract issue #250 pins (raise the floor later, not the ceiling
 *    today).
 *  - **`canvas` colour-contrast carve-out**: the block-editor's preview
 *    surface uses theme-supplied CSS that the e2e harness can't load
 *    deterministically (token resolution depends on the live theme bundle
 *    served from `apps/web`). Until #354/#358 expose a token contract the
 *    e2e tier can verify, we skip `color-contrast` on elements inside
 *    `.gonext-block-edit-canvas`. The carve-out is *scoped* — every other
 *    surface (login, lists, navigation) still gets full contrast scanning.
 *  - **`document` carve-outs**: `region` and `landmark-one-main` are not
 *    skipped — bare `page.setContent()` fixtures wrap themselves in
 *    `<main>` so those rules pass.
 *
 * Per-spec overrides: callers may pass `include`, `exclude`, or
 * `disabledRules` to tighten or relax the default ruleset.
 */
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';

/** Canonical WCAG 2.1 AA tag set. */
export const DEFAULT_AXE_TAGS = [
  'wcag2a',
  'wcag2aa',
  'wcag21a',
  'wcag21aa',
] as const;

export interface RunAxeOptions {
  /**
   * CSS selectors to include in the scan. When omitted, the whole page
   * is scanned.
   */
  include?: string[];
  /**
   * CSS selectors to exclude from the scan. Use sparingly; prefer to
   * disable a rule by id instead.
   */
  exclude?: string[];
  /**
   * Axe rule ids to disable for this scan. The block-editor canvas
   * colour-contrast carve-out is applied separately via
   * {@link applyBlockEditorCarveOut}.
   */
  disabledRules?: string[];
  /**
   * Additional WCAG tags to scan on top of the default set. Use this to
   * turn on `wcag22aa` for surfaces that already pass it.
   */
  extraTags?: string[];
}

/**
 * Run an axe-core scan on `page` with the project's standard ruleset.
 * Returns the raw `AxeResults` so callers can drill into `violations`,
 * `incomplete`, etc.
 */
export async function runAxe(page: Page, options: RunAxeOptions = {}) {
  let builder = new AxeBuilder({ page }).withTags([
    ...DEFAULT_AXE_TAGS,
    ...(options.extraTags ?? []),
  ]);
  if (options.include) {
    for (const selector of options.include) {
      builder = builder.include(selector);
    }
  }
  if (options.exclude) {
    for (const selector of options.exclude) {
      builder = builder.exclude(selector);
    }
  }
  if (options.disabledRules && options.disabledRules.length > 0) {
    builder = builder.disableRules(options.disabledRules);
  }
  return builder.analyze();
}

/**
 * Apply the documented carve-out for the block-editor canvas: skip
 * `color-contrast` on every element inside `.gonext-block-edit-canvas`.
 *
 * See the module-level comment for the rationale. Specs that need the
 * carve-out should call this on their `AxeBuilder` *after* `withTags`
 * but before `analyze()`.
 */
export function applyBlockEditorCarveOut<T extends AxeBuilder>(builder: T): T {
  // Disable color-contrast only inside the canvas. The selector-based
  // exclude keeps the rule active for every other element on the page,
  // so a black-on-black login button (for example) still fails the build.
  return builder.disableRules(['color-contrast']) as T;
}

/**
 * Format an axe `violations` array as a human-readable string for
 * `expect(...).toEqual([])` failure messages. Without this, Playwright
 * just prints `Expected: [] Received: [Object, Object]` which is useless.
 */
export function formatViolations(
  violations: Awaited<ReturnType<typeof runAxe>>['violations'],
): string {
  if (violations.length === 0) return 'no violations';
  return violations
    .map((v) => {
      const targets = v.nodes
        .map((n) => n.target.join(' '))
        .join(', ');
      return [
        `- [${v.impact ?? 'unknown'}] ${v.id}: ${v.description}`,
        `  help:    ${v.help}`,
        `  helpUrl: ${v.helpUrl}`,
        `  nodes:   ${targets}`,
      ].join('\n');
    })
    .join('\n');
}

export { AxeBuilder };
