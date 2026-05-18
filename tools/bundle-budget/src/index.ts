/**
 * @gonext/bundle-budget public surface.
 *
 * Exports the manifest parser, budget loader, and the top-level check
 * orchestrator. Most callers want `runCheck` (re-exported from `./check`);
 * the lower-level parsers are exported for tests and for future tools that
 * may want to consume route sizes without imposing pass/fail semantics.
 */
export {
  parseManifest,
  computeRouteSizes,
  gzippedSize,
  type Manifest,
  type RouteSize,
  type AssetKind,
} from './manifest.ts';

export {
  loadBudgets,
  parseBudgets,
  type Budgets,
  type RouteBudget,
  type GlobalBudget,
} from './budget.ts';

export {
  checkBudgets,
  renderSummary,
  renderFailureDiff,
  runCheck,
  type CheckResult,
  type RouteCheck,
  type BudgetViolation,
} from './check.ts';
