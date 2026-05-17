/**
 * `BlockRegistry` — the runtime map from block-type names to their
 * `BlockTypeDefinition`. Plugins call `register()` at bundle init; the
 * editor and the server-side renderer call `get()` to dispatch.
 *
 * Design notes:
 *
 *  - **Registration is single-write.** Calling `register()` twice with the
 *    same name throws by default — silent overwrites caused real bugs in
 *    Gutenberg. The escape hatch is `replace: true`, used by HMR.
 *  - **Validation is owned here.** The registry composes the same
 *    `BlockValidator` that lives in `validator.ts` so consumers don't have
 *    to wire it up themselves.
 *  - **Compiled attribute schemas are invalidated on re-registration.**
 *    The validator's internal cache would otherwise survive an HMR-driven
 *    schema change.
 */

import type {
  BlockAttributes,
  BlockTree,
  BlockTypeDefinition,
  ValidationResult,
} from './types.ts';
import { assertPinnedDialect, BlockValidator } from './validator.ts';

export interface RegisterOptions {
  /**
   * Allow overwriting an existing registration. Defaults to false. The
   * editor's HMR loader sets this to true; production code should leave it
   * alone so collisions surface loudly.
   */
  replace?: boolean;
}

export class DuplicateBlockTypeError extends Error {
  public override readonly name = 'DuplicateBlockTypeError';
  public readonly blockType: string;

  constructor(blockType: string) {
    super(
      `block type "${blockType}" is already registered. ` +
        `Pass { replace: true } to overwrite (HMR only).`,
    );
    this.blockType = blockType;
  }
}

export class BlockRegistry {
  private readonly defs = new Map<string, BlockTypeDefinition>();
  private readonly validator: BlockValidator;

  constructor() {
    // The lookup closure picks up changes as `defs` mutates — no need to
    // rebuild the validator on each register call.
    this.validator = new BlockValidator((name) => this.defs.get(name));
  }

  /**
   * Register a block type. Throws `DuplicateBlockTypeError` on a collision
   * unless `options.replace` is true.
   */
  register<A extends BlockAttributes>(
    def: BlockTypeDefinition<A>,
    options: RegisterOptions = {},
  ): void {
    // Dialect pin: a block whose attribute schema declares a draft
    // other than 2020-12 is rejected here, before it ever reaches the
    // validator's compile cache. We do this BEFORE the duplicate check
    // so a buggy HMR-driven re-register can't silently install a
    // mis-dialect schema if someone passes `replace: true`. See issue
    // #275 and `UnsupportedDialectError` for the contract.
    assertPinnedDialect(def.attributes, def.name);
    const existing = this.defs.get(def.name);
    if (existing !== undefined && options.replace !== true) {
      throw new DuplicateBlockTypeError(def.name);
    }
    if (existing !== undefined) {
      // Drop the stale compiled attribute schema before we swap the def.
      this.validator.invalidate(def.name);
    }
    this.defs.set(def.name, def as BlockTypeDefinition);
  }

  /** Look up a registered block type by name. `undefined` if not found. */
  get(name: string): BlockTypeDefinition | undefined {
    return this.defs.get(name);
  }

  /** Snapshot list of every registered block type, in registration order. */
  list(): BlockTypeDefinition[] {
    return [...this.defs.values()];
  }

  /** Returns true if a block type is registered under `name`. */
  has(name: string): boolean {
    return this.defs.has(name);
  }

  /**
   * Remove a registration. Returns true if something was removed. Mostly
   * useful in tests and HMR teardown.
   */
  unregister(name: string): boolean {
    const had = this.defs.delete(name);
    if (had) {
      this.validator.invalidate(name);
    }
    return had;
  }

  /** Drop every registered block. Test-only convenience. */
  clear(): void {
    for (const name of this.defs.keys()) {
      this.validator.invalidate(name);
    }
    this.defs.clear();
  }

  /**
   * Validate a block tree against the registered types. See
   * `BlockValidator` for the algorithm and `ValidationResult` for the
   * return shape. Never throws.
   */
  validate(tree: BlockTree | unknown): ValidationResult {
    return this.validator.validate(tree);
  }
}
