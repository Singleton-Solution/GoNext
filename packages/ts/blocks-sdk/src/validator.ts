/**
 * Block tree validator.
 *
 * Two-pass design:
 *
 *  1. **Structural** — does the input match `BlockJSONSchema`? (Each block
 *     has a `type`, an `attributes` object, optional `innerBlocks` array
 *     that itself matches the same schema, etc.)
 *  2. **Semantic** — for each registered block type, do the attributes
 *     match the type's own JSON Schema? Unknown block types surface as a
 *     dedicated `unknown-type` error so the caller can decide whether to
 *     hard-fail or strip them.
 *
 * The validator NEVER throws. All failures land in
 * `ValidationResult.errors` with JSON-pointer-style paths. The save flow
 * (server-side) hard-fails on any error; the editor uses these messages to
 * highlight problem blocks inline.
 */

import Ajv, { type ValidateFunction } from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import {
  BlockJSONSchema,
  BLOCK_SCHEMA_ID,
  BlockTreeJSONSchema,
  BLOCK_TREE_SCHEMA_ID,
  SCHEMA_DIALECT,
} from './schema.ts';
import type {
  AttributesSchema,
  Block,
  BlockTree,
  BlockTypeDefinition,
  ValidationError,
  ValidationResult,
} from './types.ts';

/**
 * Thrown when a per-block attribute schema declares a `$schema` URL that
 * isn't the pinned 2020-12 dialect. See `SCHEMA_DIALECT` for the policy
 * rationale (issue #275). The plugin host bubbles this up to the
 * operator as an install-time error, paired with the Go-side
 * `jsonschemautil.ErrUnsupportedDialect` so the message is consistent
 * across the stack.
 */
export class UnsupportedDialectError extends Error {
  public override readonly name = 'UnsupportedDialectError';
  public readonly declared: string;
  public readonly blockType?: string;

  constructor(declared: string, blockType?: string) {
    const suffix = blockType !== undefined ? ` for block "${blockType}"` : '';
    super(
      `blocks-sdk: schema${suffix} declared $schema=${JSON.stringify(declared)}, ` +
        `but only ${SCHEMA_DIALECT} is accepted. Rewrite the schema under ` +
        `JSON Schema 2020-12 (see docs/02-plugin-system.md §7.7).`,
    );
    this.declared = declared;
    if (blockType !== undefined) this.blockType = blockType;
  }
}

/**
 * Inspect a candidate schema for a top-level `$schema` field. If present
 * and not the pinned dialect, throw `UnsupportedDialectError`. Absent
 * `$schema` is accepted — Ajv2020 treats the document as 2020-12 by
 * default, matching the policy in `packages/go/jsonschemautil`.
 *
 * Exported so the registry (`BlockRegistry.register`) can run the check
 * at registration time, before the offending schema reaches Ajv's
 * compile step where the error would be less obvious.
 */
export function assertPinnedDialect(
  schema: unknown,
  blockType?: string,
): void {
  if (
    schema === null ||
    typeof schema !== 'object' ||
    Array.isArray(schema)
  ) {
    // Non-object schemas will fail Ajv's own checks with a clearer
    // message; the dialect rule is meaningless for them.
    return;
  }
  const $schema = (schema as { $schema?: unknown }).$schema;
  if ($schema === undefined) return;
  if (typeof $schema !== 'string' || $schema.trim() !== SCHEMA_DIALECT) {
    throw new UnsupportedDialectError(String($schema), blockType);
  }
}

/**
 * Lookup contract used by the validator and the registry. Keeping this as a
 * plain function means the validator can be reused with mock registries in
 * tests without depending on the `BlockRegistry` class.
 */
export type BlockTypeLookup = (name: string) =>
  | BlockTypeDefinition
  | undefined;

/**
 * Build a fresh Ajv instance with our schemas pre-registered. We use the
 * 2020-12 build so `unevaluatedProperties`, `prefixItems`, and
 * `dependentRequired` are available to per-block attribute schemas.
 *
 * Each `BlockValidator` owns its own Ajv to avoid cross-test contamination
 * via the schema cache.
 */
function createAjv(): Ajv {
  const ajv = new Ajv({
    allErrors: true,
    strict: false,
    // Validate against the registered $id, not a per-call inline schema.
    addUsedSchema: false,
  });
  addFormats(ajv);
  // Defensive: the baseline schemas live alongside this file and are
  // versioned together, but if someone hand-edits them with a different
  // dialect we want to fail loudly at construction time rather than
  // silently swap dialect semantics under every consumer.
  assertPinnedDialect(BlockJSONSchema);
  assertPinnedDialect(BlockTreeJSONSchema);
  ajv.addSchema(BlockJSONSchema, BLOCK_SCHEMA_ID);
  ajv.addSchema(BlockTreeJSONSchema, BLOCK_TREE_SCHEMA_ID);
  return ajv;
}

/**
 * Per-attribute-schema compilation cache, keyed by block type name. Schemas
 * rarely change at runtime, so caching the compiled validator saves
 * meaningful CPU on large documents.
 */
class AttributeValidatorCache {
  private readonly ajv: Ajv;
  private readonly fns = new Map<string, ValidateFunction>();

  constructor(ajv: Ajv) {
    this.ajv = ajv;
  }

  get(typeName: string, schema: AttributesSchema): ValidateFunction {
    const cached = this.fns.get(typeName);
    if (cached !== undefined) {
      return cached;
    }
    // Enforce the pinned dialect at compile time. The registry checks
    // this on `register()` too, but the validator is a public entry
    // point — callers that construct a `BlockValidator` directly (no
    // registry) still need the guard.
    assertPinnedDialect(schema, typeName);
    const fn = this.ajv.compile(schema as Record<string, unknown>);
    this.fns.set(typeName, fn);
    return fn;
  }

  /** Drop a cached compiled validator (called when a type is re-registered). */
  drop(typeName: string): void {
    this.fns.delete(typeName);
  }
}

/**
 * Map an Ajv error to our `ValidationError` shape, rebasing the
 * `instancePath` onto the caller-supplied `basePath` so multi-block trees
 * report meaningful pointers.
 */
function ajvErrorsToValidationErrors(
  errors: NonNullable<ValidateFunction['errors']>,
  basePath: string,
  code: ValidationError['code'],
  blockType?: string,
): ValidationError[] {
  return errors.map((err) => ({
    path: `${basePath}${err.instancePath ?? ''}`,
    code,
    message: err.message ?? 'validation failed',
    ...(blockType !== undefined ? { blockType } : {}),
  }));
}

export class BlockValidator {
  private readonly ajv: Ajv;
  private readonly validateTree: ValidateFunction;
  private readonly attrCache: AttributeValidatorCache;
  private readonly lookup: BlockTypeLookup;

  constructor(lookup: BlockTypeLookup) {
    this.ajv = createAjv();
    this.lookup = lookup;
    // The root-tree validator cannot fail to compile — the schema is
    // well-formed and registered in `createAjv`. The check below is purely a
    // defensive guard in case someone hand-mutates the schema constants.
    const treeFn = this.ajv.getSchema(BLOCK_TREE_SCHEMA_ID);
    /* c8 ignore next 3 */
    if (treeFn === undefined) {
      throw new Error('blocks-sdk: failed to compile baseline block schemas');
    }
    this.validateTree = treeFn;
    this.attrCache = new AttributeValidatorCache(this.ajv);
  }

  /** Invalidate a cached compiled attribute schema, e.g. on re-registration. */
  invalidate(typeName: string): void {
    this.attrCache.drop(typeName);
  }

  /** Validate the entire tree. Always returns; never throws. */
  validate(tree: unknown): ValidationResult {
    // Pass 1: structural shape of the array + every nested block.
    const treeOk = this.validateTree(tree);
    if (!treeOk) {
      const errs = this.validateTree.errors ?? [];
      // A single bad node can fan out into multiple errors as the schema
      // climbs back out of $refs — we keep them all so the editor's
      // "highlight every bad block" UX has the data it needs.
      return {
        valid: false,
        errors: ajvErrorsToValidationErrors(errs, '', 'schema'),
      };
    }

    // Past pass 1, the input is guaranteed to be `BlockTree`-shaped.
    const errors: ValidationError[] = [];
    const blockTree = tree as BlockTree;
    blockTree.forEach((block, idx) => {
      errors.push(...this.collectErrors(block, `/${idx}`));
    });

    return { valid: errors.length === 0, errors };
  }

  /**
   * Walk a single block subtree, gathering attribute/unknown-type errors.
   * Structural errors are NOT re-reported here — pass 1 in `validate()` has
   * already caught those.
   */
  private collectErrors(block: unknown, basePath: string): ValidationError[] {
    const errors: ValidationError[] = [];
    const node = block as Block;
    const def = this.lookup(node.type);

    if (def === undefined) {
      errors.push({
        path: basePath,
        code: 'unknown-type',
        message: `unknown block type: ${node.type}`,
        blockType: node.type,
      });
      // Still walk children — they may be registered even if the parent
      // isn't.
    } else {
      const attrFn = this.attrCache.get(def.name, def.attributes);
      const attrOk = attrFn(node.attributes);
      if (!attrOk) {
        const errs = attrFn.errors ?? [];
        errors.push(
          ...ajvErrorsToValidationErrors(
            errs,
            `${basePath}/attributes`,
            'attributes',
            node.type,
          ),
        );
      }
    }

    if (node.innerBlocks !== undefined) {
      node.innerBlocks.forEach((child, idx) => {
        errors.push(
          ...this.collectErrors(child, `${basePath}/innerBlocks/${idx}`),
        );
      });
    }

    return errors;
  }
}

/**
 * Convenience: validate a tree against a one-shot lookup function without
 * constructing a long-lived `BlockValidator`. Useful for ad-hoc validation
 * in scripts. Production code should reuse a single `BlockValidator`.
 */
export function validateBlockTree(
  tree: unknown,
  lookup: BlockTypeLookup,
): ValidationResult {
  return new BlockValidator(lookup).validate(tree);
}
