/**
 * Internal helper: run axe-core against a DOM container in unit tests.
 *
 * Issue #250 — every interactive surface must score WCAG 2.1 AA-clean.
 * Each core block's `*.test.tsx` adds an axe scan as a new test case
 * asserting `violations.length === 0`. Sharing one tiny helper here
 * keeps the per-block boilerplate to a single line:
 *
 *   it('has no axe a11y violations when rendered', async () => {
 *     const { container } = render(<FooEdit ... />);
 *     await assertNoAxeViolations(container);
 *   });
 *
 * Decisions:
 *  - **Standards**: WCAG 2.1 AA (`wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa`).
 *    Same tag set as the e2e helper in `tools/e2e/tests/a11y/helpers/axe.ts`
 *    so unit-level and e2e-level scans agree.
 *  - **`color-contrast`**: disabled. jsdom doesn't apply CSS, so axe-core
 *    can't compute real contrast ratios in this environment — the
 *    contract is enforced by the Playwright a11y suite. Leaving the rule
 *    on here would emit spurious "incomplete" results.
 *  - **`region`**: disabled. Each test renders a single block in isolation
 *    without a surrounding `<main>` landmark, so the landmark rules would
 *    fail by construction. The full-page landmark check belongs to the
 *    e2e tier.
 */
import axe, { type AxeResults, type RunOptions } from 'axe-core';
import { expect } from 'vitest';

const DEFAULT_OPTIONS: RunOptions = {
  runOnly: {
    type: 'tag',
    values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'],
  },
  rules: {
    // See header — jsdom doesn't load CSS, so contrast is checked at the
    // e2e tier instead.
    'color-contrast': { enabled: false },
    // Single-block fixtures don't have a `<main>` landmark; the editor
    // canvas e2e spec asserts the surrounding landmark structure.
    region: { enabled: false },
  },
};

/**
 * Run axe-core on `node` (typically the `container` from `render()`) and
 * format any violations into a single readable string before failing.
 * The formatted message makes debugging in CI tractable — a raw
 * `Expected: [] Received: [Object]` is not enough to act on.
 */
export async function assertNoAxeViolations(
  node: Element,
  overrideOptions?: RunOptions,
): Promise<void> {
  const results: AxeResults = await axe.run(node, {
    ...DEFAULT_OPTIONS,
    ...overrideOptions,
  });
  expect(
    results.violations,
    formatViolations(results.violations),
  ).toHaveLength(0);
}

function formatViolations(violations: AxeResults['violations']): string {
  if (violations.length === 0) return 'no axe violations';
  return violations
    .map((v) => {
      const targets = v.nodes.map((n) => n.target.join(' ')).join(', ');
      return [
        `- [${v.impact ?? 'unknown'}] ${v.id}: ${v.description}`,
        `  help:    ${v.help}`,
        `  helpUrl: ${v.helpUrl}`,
        `  nodes:   ${targets}`,
      ].join('\n');
    })
    .join('\n');
}
