/**
 * Budget enforcement entry point.
 *
 * `runCheck` is the function the CLI wraps: given an app name, a `.next`
 * directory, and a parsed budgets file, it returns a `CheckResult` carrying
 * the pass/fail verdict, per-route summaries, and rendered markdown for the
 * PR comment / step summary. Designed to be deterministic — same inputs
 * always produce the same output bytes so CI diffs stay sane.
 */
import {
  computeRouteSizes,
  parseManifest,
  type RouteSize,
} from './manifest.ts';
import {
  findRouteBudget,
  loadBudgets,
  type Budgets,
  type RouteBudget,
} from './budget.ts';

export interface BudgetViolation {
  kind: 'js' | 'css' | 'font' | 'transfer';
  /** Actual size in KB (gzipped). */
  actualKb: number;
  /** Declared ceiling in KB. */
  budgetKb: number;
  /** Signed delta — positive when over budget. */
  overByKb: number;
}

export interface RouteCheck {
  route: string;
  /** Workspace name (e.g. `admin`). */
  app: string;
  /** Computed sizes — pass-through from `computeRouteSizes`. */
  sizes: RouteSize;
  /** Matched budget, or `undefined` if the route has no declared budget. */
  budget?: RouteBudget;
  violations: BudgetViolation[];
  /** True when no declared budget covers this route. */
  warning?: string;
}

export interface CheckResult {
  app: string;
  passed: boolean;
  routes: RouteCheck[];
  warnings: string[];
  summaryMarkdown: string;
  /** Markdown rendered only on failure; empty string on pass. */
  failureMarkdown: string;
}

const BYTES_PER_KB = 1000;

function toKb(bytes: number): number {
  // One decimal — matches what `gzip-size` and similar tools render. We don't
  // round to integers because a 0.4 KB overflow is meaningful at these scales.
  return Math.round((bytes / BYTES_PER_KB) * 10) / 10;
}

/**
 * Apply the budget rules to a list of computed route sizes. Returns a list of
 * `RouteCheck` entries in the same order as the input.
 */
export function checkBudgets(
  app: string,
  sizes: RouteSize[],
  budgets: Budgets,
): { routes: RouteCheck[]; warnings: string[] } {
  const warnings: string[] = [];
  const routes: RouteCheck[] = sizes.map((s) => {
    const budget = findRouteBudget(budgets, app, s.route);
    const violations: BudgetViolation[] = [];

    if (budget) {
      const jsKb = toKb(s.byKind.js);
      if (jsKb > budget.maxJsKb) {
        violations.push({
          kind: 'js',
          actualKb: jsKb,
          budgetKb: budget.maxJsKb,
          overByKb: Math.round((jsKb - budget.maxJsKb) * 10) / 10,
        });
      }
      const cssKb = toKb(s.byKind.css);
      if (cssKb > budget.maxCssKb) {
        violations.push({
          kind: 'css',
          actualKb: cssKb,
          budgetKb: budget.maxCssKb,
          overByKb: Math.round((cssKb - budget.maxCssKb) * 10) / 10,
        });
      }
      if (budget.maxFontKb !== undefined) {
        const fontKb = toKb(s.byKind.font);
        if (fontKb > budget.maxFontKb) {
          violations.push({
            kind: 'font',
            actualKb: fontKb,
            budgetKb: budget.maxFontKb,
            overByKb: Math.round((fontKb - budget.maxFontKb) * 10) / 10,
          });
        }
      }
    } else {
      // Missing budget is advisory — a warning, never a failure. New routes
      // appear before the human team gets a chance to declare a budget; we
      // surface them so they don't slip through silently.
      warnings.push(
        `No budget declared for ${app}:${s.route} ` +
          `(JS ${toKb(s.byKind.js)} KB, CSS ${toKb(s.byKind.css)} KB)`,
      );
    }

    // Global transfer cap applies to every route regardless of whether a
    // per-route budget exists.
    const totalKb = toKb(s.totalBytes);
    if (totalKb > budgets.global.maxTransferKb) {
      violations.push({
        kind: 'transfer',
        actualKb: totalKb,
        budgetKb: budgets.global.maxTransferKb,
        overByKb:
          Math.round((totalKb - budgets.global.maxTransferKb) * 10) / 10,
      });
    }

    const check: RouteCheck = {
      route: s.route,
      app,
      sizes: s,
      violations,
    };
    if (budget) check.budget = budget;
    if (!budget) check.warning = `No budget declared for ${s.route}`;
    return check;
  });

  return { routes, warnings };
}

/**
 * Render a per-route markdown table. Stable column widths and ordering so the
 * output diffs cleanly across runs — the CI summary is most useful when small
 * changes only highlight what actually moved.
 */
export function renderSummary(result: {
  app: string;
  routes: RouteCheck[];
  warnings: string[];
}): string {
  const lines: string[] = [];
  lines.push(`## Bundle budget report — ${result.app}`);
  lines.push('');
  lines.push(
    '| Route | JS (KB) | CSS (KB) | Font (KB) | Total (KB) | Status |',
  );
  lines.push(
    '| --- | ---: | ---: | ---: | ---: | :---: |',
  );

  for (const r of result.routes) {
    const js = toKb(r.sizes.byKind.js);
    const css = toKb(r.sizes.byKind.css);
    const font = toKb(r.sizes.byKind.font);
    const total = toKb(r.sizes.totalBytes);

    let status: string;
    if (r.violations.length > 0) {
      status = 'FAIL';
    } else if (!r.budget) {
      status = 'WARN';
    } else {
      status = 'OK';
    }

    const fmtJs = r.budget
      ? `${js} / ${r.budget.maxJsKb}`
      : `${js}`;
    const fmtCss = r.budget
      ? `${css} / ${r.budget.maxCssKb}`
      : `${css}`;
    const fmtFont =
      r.budget && r.budget.maxFontKb !== undefined
        ? `${font} / ${r.budget.maxFontKb}`
        : `${font}`;

    lines.push(
      `| \`${r.route}\` | ${fmtJs} | ${fmtCss} | ${fmtFont} | ${total} | ${status} |`,
    );
  }

  if (result.warnings.length > 0) {
    lines.push('');
    lines.push('### Warnings');
    lines.push('');
    for (const w of result.warnings) {
      lines.push(`- ${w}`);
    }
  }
  lines.push('');
  return lines.join('\n');
}

/**
 * Render a failure-only diff that names exactly which routes broke which
 * caps. Designed to be the first thing the engineer sees on a failed CI run —
 * actionable at a glance, no markdown table parsing required.
 */
export function renderFailureDiff(result: {
  app: string;
  routes: RouteCheck[];
}): string {
  const failed = result.routes.filter((r) => r.violations.length > 0);
  if (failed.length === 0) return '';

  const lines: string[] = [];
  lines.push(`### Budget violations — ${result.app}`);
  lines.push('');
  for (const r of failed) {
    lines.push(`**\`${r.route}\`**`);
    for (const v of r.violations) {
      const label =
        v.kind === 'transfer' ? 'total transfer' : `${v.kind} bundle`;
      lines.push(
        `- ${label}: **${v.actualKb} KB** (budget ${v.budgetKb} KB, ` +
          `over by ${v.overByKb} KB)`,
      );
    }
    lines.push('');
  }
  return lines.join('\n');
}

/**
 * Top-level orchestrator. Reads the manifest, computes sizes, checks budgets,
 * and renders the markdown. The CLI just calls this and decides the exit
 * code from `passed`.
 */
export function runCheck(opts: {
  app: string;
  nextDir: string;
  budgetsPath: string;
}): CheckResult {
  const manifest = parseManifest(opts.nextDir);
  const sizes = computeRouteSizes(manifest, opts.nextDir);
  const budgets = loadBudgets(opts.budgetsPath);
  const { routes, warnings } = checkBudgets(opts.app, sizes, budgets);

  const passed = routes.every((r) => r.violations.length === 0);
  const summaryMarkdown = renderSummary({ app: opts.app, routes, warnings });
  const failureMarkdown = passed
    ? ''
    : renderFailureDiff({ app: opts.app, routes });

  return {
    app: opts.app,
    passed,
    routes,
    warnings,
    summaryMarkdown,
    failureMarkdown,
  };
}
