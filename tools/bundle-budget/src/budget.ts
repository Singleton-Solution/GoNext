/**
 * Budget declaration loader.
 *
 * The on-disk format is YAML — chosen because it's the same format the
 * CONTRIBUTING examples use for other contracts and is the most human-friendly
 * for an "edit this when you raise a budget" file. The schema is intentionally
 * tight (only the keys the checker understands) so a typo fails fast.
 */
import * as fs from 'node:fs';
import * as yaml from 'js-yaml';

export interface GlobalBudget {
  /**
   * Total transferred KB ceiling applied to every route. Combines all asset
   * classes; if a route's JS + CSS + fonts + other exceeds this, it fails
   * regardless of the per-class caps.
   */
  maxTransferKb: number;
}

export interface RouteBudget {
  /** Workspace name without the `@gonext/` prefix, e.g. `admin` or `web`. */
  app: string;
  /** App Router segment, e.g. `/` or `/posts/[id]/edit`. */
  route: string;
  maxJsKb: number;
  maxCssKb: number;
  /** Optional — font weight isn't always meaningfully separable from JS. */
  maxFontKb?: number;
}

export interface Budgets {
  global: GlobalBudget;
  routes: RouteBudget[];
}

/**
 * Parse a budgets YAML string into the strongly-typed shape. Throws with a
 * helpful message if a required field is missing or malformed; callers (the
 * CLI in particular) should not try to recover from these — a malformed
 * budget file is a deploy-blocking error, not a soft warning.
 */
export function parseBudgets(source: string): Budgets {
  const raw = yaml.load(source);
  if (raw === null || typeof raw !== 'object') {
    throw new Error('budgets.yaml is empty or not a mapping');
  }
  const obj = raw as Record<string, unknown>;

  const globalRaw = obj.global;
  if (!globalRaw || typeof globalRaw !== 'object') {
    throw new Error("budgets.yaml is missing a 'global' section");
  }
  const globalObj = globalRaw as Record<string, unknown>;
  if (typeof globalObj.maxTransferKb !== 'number') {
    throw new Error("budgets.yaml: global.maxTransferKb must be a number");
  }
  const global: GlobalBudget = {
    maxTransferKb: globalObj.maxTransferKb,
  };

  const routesRaw = obj.routes;
  if (!Array.isArray(routesRaw)) {
    throw new Error("budgets.yaml: 'routes' must be a list");
  }
  const routes: RouteBudget[] = routesRaw.map((entry, idx) => {
    if (!entry || typeof entry !== 'object') {
      throw new Error(`budgets.yaml routes[${idx}] is not a mapping`);
    }
    const r = entry as Record<string, unknown>;
    if (typeof r.app !== 'string') {
      throw new Error(`budgets.yaml routes[${idx}].app must be a string`);
    }
    if (typeof r.route !== 'string') {
      throw new Error(`budgets.yaml routes[${idx}].route must be a string`);
    }
    if (typeof r.maxJsKb !== 'number') {
      throw new Error(
        `budgets.yaml routes[${idx}].maxJsKb must be a number`,
      );
    }
    if (typeof r.maxCssKb !== 'number') {
      throw new Error(
        `budgets.yaml routes[${idx}].maxCssKb must be a number`,
      );
    }
    const out: RouteBudget = {
      app: r.app,
      route: r.route,
      maxJsKb: r.maxJsKb,
      maxCssKb: r.maxCssKb,
    };
    if (typeof r.maxFontKb === 'number') {
      out.maxFontKb = r.maxFontKb;
    }
    return out;
  });

  return { global, routes };
}

/**
 * Convenience wrapper that reads a budgets file from disk.
 */
export function loadBudgets(filePath: string): Budgets {
  if (!fs.existsSync(filePath)) {
    throw new Error(`budgets file not found: ${filePath}`);
  }
  const source = fs.readFileSync(filePath, 'utf8');
  return parseBudgets(source);
}

/**
 * Find the budget for a given app + route, if one exists. Routes are matched
 * exactly — Next.js dynamic segments stay as `[id]` placeholders both in the
 * manifest and in the budget file, so equality works without templating.
 */
export function findRouteBudget(
  budgets: Budgets,
  app: string,
  route: string,
): RouteBudget | undefined {
  return budgets.routes.find((r) => r.app === app && r.route === route);
}
