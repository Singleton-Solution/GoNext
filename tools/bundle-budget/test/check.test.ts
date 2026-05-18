/**
 * Tests for the budget checker — pass/fail rules, markdown rendering, and
 * the warning vs. error distinction for routes without a declared budget.
 */
import { describe, it, expect } from 'vitest';
import {
  checkBudgets,
  renderFailureDiff,
  renderSummary,
} from '../src/check.ts';
import { parseBudgets } from '../src/budget.ts';
import type { RouteSize } from '../src/manifest.ts';

const budgets = parseBudgets(`
global:
  maxTransferKb: 1000
routes:
  - app: admin
    route: /
    maxJsKb: 250
    maxCssKb: 40
    maxFontKb: 100
  - app: admin
    route: /posts/[id]/edit
    maxJsKb: 600
    maxCssKb: 60
`);

function size(
  route: string,
  jsKb: number,
  cssKb: number,
  fontKb = 0,
): RouteSize {
  // 1 KB == 1000 bytes for the budget math.
  return {
    route,
    totalBytes: (jsKb + cssKb + fontKb) * 1000,
    byKind: {
      js: jsKb * 1000,
      css: cssKb * 1000,
      font: fontKb * 1000,
      other: 0,
    },
    files: [],
  };
}

describe('checkBudgets', () => {
  it('passes when every route is under its budget', () => {
    const result = checkBudgets(
      'admin',
      [size('/', 200, 30, 50), size('/posts/[id]/edit', 500, 50)],
      budgets,
    );
    expect(result.warnings).toEqual([]);
    expect(result.routes.every((r) => r.violations.length === 0)).toBe(
      true,
    );
  });

  it('fails when JS exceeds the per-route cap', () => {
    const result = checkBudgets(
      'admin',
      [size('/', 300, 30)], // 300 > 250
      budgets,
    );
    const homepage = result.routes[0]!;
    expect(homepage.violations).toHaveLength(1);
    expect(homepage.violations[0]?.kind).toBe('js');
    expect(homepage.violations[0]?.overByKb).toBeCloseTo(50, 1);
  });

  it('fails when CSS exceeds the per-route cap', () => {
    const result = checkBudgets('admin', [size('/', 100, 100)], budgets);
    expect(result.routes[0]?.violations.map((v) => v.kind)).toEqual([
      'css',
    ]);
  });

  it('fails when fonts exceed the optional font cap', () => {
    const result = checkBudgets('admin', [size('/', 100, 20, 200)], budgets);
    expect(result.routes[0]?.violations.map((v) => v.kind)).toContain(
      'font',
    );
  });

  it('does not fail on fonts when no font cap is declared', () => {
    // /posts/[id]/edit has no maxFontKb in the test budget.
    const result = checkBudgets(
      'admin',
      [size('/posts/[id]/edit', 100, 20, 5000)],
      budgets,
    );
    const fontViolation = result.routes[0]?.violations.find(
      (v) => v.kind === 'font',
    );
    expect(fontViolation).toBeUndefined();
  });

  it('fails when global transfer cap is exceeded', () => {
    // 800 + 50 + 200 = 1050 KB > 1000 KB global cap, but JS/CSS individually
    // would pass the editor route's caps.
    const result = checkBudgets(
      'admin',
      [size('/posts/[id]/edit', 800, 50, 200)],
      budgets,
    );
    const kinds = result.routes[0]?.violations.map((v) => v.kind);
    expect(kinds).toContain('transfer');
  });

  it('emits a warning (not an error) for routes with no budget', () => {
    const result = checkBudgets(
      'admin',
      [size('/unknown-route', 100, 20)],
      budgets,
    );
    expect(result.warnings.length).toBe(1);
    expect(result.warnings[0]).toMatch(/admin:\/unknown-route/);
    expect(result.routes[0]?.violations).toEqual([]);
    expect(result.routes[0]?.warning).toBeDefined();
  });

  it('still applies the global transfer cap to unbudgeted routes', () => {
    // No per-route budget — but the global cap of 1000 KB still bites.
    const result = checkBudgets(
      'admin',
      [size('/unknown', 900, 200)],
      budgets,
    );
    const kinds = result.routes[0]?.violations.map((v) => v.kind);
    expect(kinds).toEqual(['transfer']);
  });

  it('reports multiple violations on the same route', () => {
    const result = checkBudgets('admin', [size('/', 300, 50, 200)], budgets);
    const kinds = result.routes[0]?.violations.map((v) => v.kind).sort();
    expect(kinds).toEqual(['css', 'font', 'js']);
  });
});

describe('renderSummary', () => {
  it('renders a stable markdown table', () => {
    const result = checkBudgets(
      'admin',
      [
        size('/', 200, 30, 50),
        size('/posts/[id]/edit', 500, 50),
      ],
      budgets,
    );
    const md = renderSummary({
      app: 'admin',
      routes: result.routes,
      warnings: result.warnings,
    });

    // Snapshot the exact text — small changes here are intentional and
    // should be reviewed alongside any code change.
    expect(md).toBe(
      `## Bundle budget report — admin

| Route | JS (KB) | CSS (KB) | Font (KB) | Total (KB) | Status |
| --- | ---: | ---: | ---: | ---: | :---: |
| \`/\` | 200 / 250 | 30 / 40 | 50 / 100 | 280 | OK |
| \`/posts/[id]/edit\` | 500 / 600 | 50 / 60 | 0 | 550 | OK |
`,
    );
  });

  it('marks an unbudgeted route as WARN and lists the warning', () => {
    const result = checkBudgets(
      'admin',
      [size('/unknown', 50, 10)],
      budgets,
    );
    const md = renderSummary({
      app: 'admin',
      routes: result.routes,
      warnings: result.warnings,
    });
    expect(md).toContain('WARN');
    expect(md).toContain('### Warnings');
    expect(md).toContain('No budget declared');
  });

  it('marks a violating route as FAIL', () => {
    const result = checkBudgets('admin', [size('/', 300, 50)], budgets);
    const md = renderSummary({
      app: 'admin',
      routes: result.routes,
      warnings: result.warnings,
    });
    expect(md).toContain('FAIL');
  });
});

describe('renderFailureDiff', () => {
  it('returns empty string when nothing failed', () => {
    const result = checkBudgets('admin', [size('/', 100, 20)], budgets);
    expect(
      renderFailureDiff({ app: 'admin', routes: result.routes }),
    ).toBe('');
  });

  it('lists each violation with kind, actual, budget, and delta', () => {
    const result = checkBudgets('admin', [size('/', 300, 50)], budgets);
    const diff = renderFailureDiff({
      app: 'admin',
      routes: result.routes,
    });
    expect(diff).toContain('Budget violations — admin');
    expect(diff).toContain('js bundle');
    expect(diff).toContain('css bundle');
    expect(diff).toContain('over by');
  });

  it('labels transfer violations distinctly', () => {
    const result = checkBudgets('admin', [size('/', 900, 200)], budgets);
    const diff = renderFailureDiff({
      app: 'admin',
      routes: result.routes,
    });
    expect(diff).toContain('total transfer');
  });
});
