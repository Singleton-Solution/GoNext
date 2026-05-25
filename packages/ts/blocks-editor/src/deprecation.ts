/**
 * Block-deprecation pipeline — issue #198.
 *
 * When a block author increments their definition's `version` field
 * (and appends a new `deprecated[]` entry), previously-saved posts
 * contain the OLD attribute shape. The migrator (`migrateBlockTree`
 * in @gonext/blocks-sdk) walks every block on load and upgrades it
 * in place.
 *
 * This module wraps the SDK pipeline with editor-side concerns:
 *
 *  - `runDeprecations(tree, registry)` migrates a tree against a
 *    BlockRegistry — the standard wiring the canvas calls at load.
 *  - `auditDeprecations(tree, registry)` walks the tree WITHOUT
 *    mutating it and returns a flat list of every deprecated node.
 *    The inspector calls this to surface a per-block warning chip;
 *    the global "N blocks need upgrading" banner uses the count.
 *  - `warnDeprecatedBlocks` logs (dev-only) a structured warning so
 *    block authors notice when their migration didn't fire — common
 *    cause: stale `isEligible()` predicate after a schema bump.
 *
 * The SDK functions are pure and side-effect-free; this module owns
 * the editor-side console log + DOM warning surface.
 */

import {
  detectBlockDeprecation,
  migrateBlockTree,
  type Block,
  type BlockRegistry,
  type BlockTree,
} from '@gonext/blocks-sdk';

/**
 * One node-level audit entry. Carries enough context for the
 * inspector banner ("v1 needs upgrade to v3") and for the dev
 * console warning to point at the specific block in a large tree.
 */
export interface DeprecationFinding {
  /** The block that triggered the finding. */
  block: Block;
  /**
   * Index-path through the tree, root-out. `[0, 2, 1]` is "the
   * second child of the third child of the first root block". Same
   * shape the canvas's UnknownBlock placeholder uses.
   */
  path: number[];
  /** The version of the matched deprecation entry, if declared. */
  fromVersion?: number;
  /** The block-type definition's current version, if declared. */
  toVersion?: number;
}

/**
 * Run the deprecation pipeline against a tree, using the registry to
 * look up each block's definition. Returns a NEW tree — the input is
 * not mutated. This is the call the editor makes when it loads a
 * post into the canvas.
 *
 * Unknown block types pass through unchanged (matches the SDK's
 * `migrateBlockTree` contract).
 */
export function runDeprecations(
  tree: BlockTree,
  registry: BlockRegistry,
): BlockTree {
  return migrateBlockTree(tree, (name) => registry.get(name));
}

/**
 * Walk the tree without mutating it. Returns every block that the
 * migrator *would* upgrade — i.e. every node whose current shape
 * matches one of its definition's `deprecated[]` entries.
 *
 * Order is depth-first, parents-before-children, matching the visual
 * order in the canvas. The inspector can present the list directly
 * as a "things to upgrade" panel.
 */
export function auditDeprecations(
  tree: BlockTree,
  registry: BlockRegistry,
): DeprecationFinding[] {
  const out: DeprecationFinding[] = [];
  walkAudit(tree, registry, [], out);
  return out;
}

function walkAudit(
  tree: BlockTree,
  registry: BlockRegistry,
  path: number[],
  out: DeprecationFinding[],
): void {
  for (let i = 0; i < tree.length; i++) {
    const block = tree[i];
    if (block === undefined) continue;
    const here = [...path, i];
    const def = registry.get(block.type);
    if (def !== undefined) {
      const detection = detectBlockDeprecation(block, def);
      if (detection.deprecated) {
        out.push({
          block,
          path: here,
          fromVersion: detection.fromVersion,
          toVersion: detection.toVersion,
        });
      }
    }
    if (block.innerBlocks !== undefined && block.innerBlocks.length > 0) {
      walkAudit(block.innerBlocks, registry, here, out);
    }
  }
}

/**
 * Emit a `console.warn` listing every deprecated block in the tree.
 * Production swallows this (NODE_ENV check) so authors see the
 * warning during development without flooding production logs.
 *
 * The call site is the canvas (on first render) — block authors who
 * forgot to update an old deprecation entry after a schema bump
 * notice during their normal edit-and-reload loop.
 */
export function warnDeprecatedBlocks(
  tree: BlockTree,
  registry: BlockRegistry,
): void {
  // `process` may be absent in browser bundles. The check is
  // defensive: production drops the warning, dev keeps it.
  const env =
    typeof process !== 'undefined' && process.env
      ? process.env.NODE_ENV
      : undefined;
  if (env === 'production') return;
  const findings = auditDeprecations(tree, registry);
  if (findings.length === 0) return;
  // One grouped console line keeps the dev console scannable. We
  // log the array directly so DevTools can expand each finding.
  // eslint-disable-next-line no-console
  console.warn(
    `[gonext/blocks-editor] ${findings.length} deprecated block(s) in tree. ` +
      `Call runDeprecations() to upgrade.`,
    findings,
  );
}
