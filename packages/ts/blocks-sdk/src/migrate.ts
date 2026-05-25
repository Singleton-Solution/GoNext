/**
 * Block-deprecation migration pipeline.
 *
 * When a block author changes their attribute shape, they MUST keep the old
 * shape parseable. The convention (matching Gutenberg) is to append a
 * `BlockDeprecation` entry to `BlockTypeDefinition.deprecated`, ordered
 * newest → oldest, that knows how to translate the old shape into the new.
 *
 * `migrateBlock` walks that array, picks the first entry that says "yes,
 * this is me" (via `isEligible` or, when omitted, by validating the old
 * attribute schema), and runs `migrate()`. The result becomes the new
 * block. If no entry matches — including when `deprecated` is empty or the
 * input already matches the current schema — the block is returned
 * unchanged.
 *
 * The pipeline is **idempotent**: running it twice on a fresh block is a
 * no-op, and running it on a partially-migrated chain re-applies as many
 * steps as still match. The cap on iterations (see `MAX_MIGRATION_STEPS`)
 * stops a buggy `migrate()` that produces an old shape from looping
 * forever.
 */

import Ajv from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import type {
  Block,
  BlockAttributes,
  BlockDeprecation,
  BlockTypeDefinition,
} from './types.ts';
import { assertPinnedDialect } from './validator.ts';

/**
 * Hard cap on migration steps for a single block. A correctly authored
 * deprecation chain only ever needs one or two passes; a higher count
 * usually means `migrate()` returned an old shape and we'd otherwise loop.
 */
const MAX_MIGRATION_STEPS = 16;

/**
 * One Ajv instance per process is fine for migration eligibility checks —
 * the schemas are small and `compile()` is the slow path. The cache lives
 * on a module-level WeakMap keyed by the deprecation object itself, so a
 * re-imported deprecation step doesn't poison the cache.
 */
const eligibilityAjv: Ajv = (() => {
  const a = new Ajv({ allErrors: false, strict: false });
  addFormats(a);
  return a;
})();

const eligibilityFns = new WeakMap<
  BlockDeprecation,
  (attrs: unknown) => boolean
>();

function getEligibility(dep: BlockDeprecation): (attrs: unknown) => boolean {
  const cached = eligibilityFns.get(dep);
  if (cached !== undefined) return cached;
  // Deprecation entries are also JSON Schema documents; pin them to
  // 2020-12 just like the live attribute schemas. Authors who fail to
  // update an old draft-07 deprecation chain learn at compile time
  // rather than seeing migrations silently no-op against new semantics.
  assertPinnedDialect(dep.attributes);
  const fn = eligibilityAjv.compile(dep.attributes as Record<string, unknown>);
  const wrapper = (attrs: unknown): boolean => Boolean(fn(attrs));
  eligibilityFns.set(dep, wrapper);
  return wrapper;
}

/**
 * Apply a single deprecation step to a block.
 */
function applyDeprecation(
  block: Block,
  dep: BlockDeprecation,
): Block {
  const innerBlocks = block.innerBlocks ?? [];
  const out = dep.migrate(
    block.attributes as BlockAttributes,
    innerBlocks,
  );
  const next: Block = {
    type: block.type,
    attributes: out.attributes,
  };
  // `out.innerBlocks` overrides; otherwise we preserve the original.
  const nextInner =
    out.innerBlocks !== undefined ? out.innerBlocks : block.innerBlocks;
  if (nextInner !== undefined) {
    next.innerBlocks = nextInner;
  }
  if (block.clientId !== undefined) {
    next.clientId = block.clientId;
  }
  return next;
}

/**
 * Find the first applicable deprecation step for a block. Returns
 * `undefined` when no step matches (block is already current).
 */
function findApplicableDeprecation(
  block: Block,
  def: BlockTypeDefinition,
): BlockDeprecation | undefined {
  if (def.deprecated === undefined || def.deprecated.length === 0) {
    return undefined;
  }
  for (const dep of def.deprecated) {
    if (dep.isEligible !== undefined) {
      const innerBlocks = block.innerBlocks ?? [];
      if (dep.isEligible(block.attributes, innerBlocks)) return dep;
      continue;
    }
    const check = getEligibility(dep);
    if (check(block.attributes)) return dep;
  }
  return undefined;
}

/**
 * Migrate a block through its registered deprecation chain. Returns the
 * input unchanged when no deprecation matches.
 *
 * The chain runs eagerly (deprecation → deprecation → deprecation …) until
 * no further step applies, capped at `MAX_MIGRATION_STEPS`.
 */
export function migrateBlock(
  block: Block,
  def: BlockTypeDefinition,
): Block {
  let current = block;
  for (let step = 0; step < MAX_MIGRATION_STEPS; step++) {
    const dep = findApplicableDeprecation(current, def);
    if (dep === undefined) return current;
    current = applyDeprecation(current, dep);
  }
  return current;
}

/**
 * Inspect a block against its definition and report whether the
 * migrator *would* upgrade it on the next pass.
 *
 * This is the read-only counterpart to `migrateBlock`: the editor's
 * inspector calls it to decide whether to surface the "this block is
 * deprecated, click to upgrade" banner. Returning `{deprecated: false}`
 * means the block already matches the current attribute schema (or the
 * definition has no deprecation chain at all).
 *
 * `fromVersion` is the version of the matched deprecation entry (or
 * `undefined` if the entry didn't declare one). `toVersion` is the
 * definition's current version. The banner uses these to say
 * "v1 → v3 migration available" instead of a generic hint.
 */
export function detectBlockDeprecation(
  block: Block,
  def: BlockTypeDefinition,
): { deprecated: boolean; fromVersion?: number; toVersion?: number } {
  const dep = findApplicableDeprecation(block, def);
  if (dep === undefined) {
    return { deprecated: false };
  }
  return {
    deprecated: true,
    fromVersion: dep.version,
    toVersion: def.version,
  };
}

/**
 * Migrate every block in a tree recursively, using a registry-style lookup
 * to find each block's definition.
 *
 * Unknown block types are passed through unchanged — migration is not
 * validation, and stripping unknown types is the validator's job.
 */
export function migrateBlockTree(
  tree: Block[],
  lookup: (name: string) => BlockTypeDefinition | undefined,
): Block[] {
  return tree.map((block) => migrateBlockTreeNode(block, lookup));
}

function migrateBlockTreeNode(
  block: Block,
  lookup: (name: string) => BlockTypeDefinition | undefined,
): Block {
  const def = lookup(block.type);
  const migrated = def !== undefined ? migrateBlock(block, def) : block;
  if (migrated.innerBlocks === undefined || migrated.innerBlocks.length === 0) {
    return migrated;
  }
  return {
    ...migrated,
    innerBlocks: migrated.innerBlocks.map((child) =>
      migrateBlockTreeNode(child, lookup),
    ),
  };
}
