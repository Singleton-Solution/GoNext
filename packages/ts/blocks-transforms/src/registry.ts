/**
 * `TransformRegistry` — the runtime map from transform ids to their
 * records. Mirrors the design of `BlockRegistry` (single-write, throws
 * on collision unless `replace: true`) so plugin authors who already
 * know the block registry have one less surface to learn.
 *
 * The registry exposes two lookup helpers tailored to the editor's
 * toolbar:
 *
 *  - **`from(blockName)`** — returns every transform whose `from`
 *    field equals `blockName` (the source matches), filtered by each
 *    transform's optional `isMatch` predicate when a `block` is also
 *    supplied. This is the call the "Transform to..." dropdown makes
 *    against the current selection.
 *  - **`to(blockName)`** — symmetric: every transform whose `to`
 *    field equals `blockName`. The editor currently uses `from()`, but
 *    `to()` is part of the public surface so plugin tooling that
 *    inspects "what can produce a `core/gallery`?" doesn't have to
 *    filter `list()` itself.
 *
 * The registry intentionally does NOT validate the transforms it
 * stores. Validating a transform would require knowing every target
 * block's attribute schema, and that schema lives in a different
 * registry. The host can compose a `BlockRegistry.validate()` pass on
 * the transformed output if it wants the safety net — see the
 * `registry.test.ts` "ergonomics" suite for the wired-up pattern.
 */
import type { Block } from '@gonext/blocks-sdk';
import type { Transform, TransformContext, TransformResult } from './types.ts';

export interface RegisterTransformOptions {
  /**
   * Allow overwriting an existing registration. Defaults to false. The
   * editor's HMR loader sets this to true; production code should
   * leave it alone so collisions surface loudly.
   */
  replace?: boolean;
}

export class DuplicateTransformError extends Error {
  public override readonly name = 'DuplicateTransformError';
  public readonly transformId: string;

  constructor(transformId: string) {
    super(
      `transform "${transformId}" is already registered. ` +
        `Pass { replace: true } to overwrite (HMR only).`,
    );
    this.transformId = transformId;
  }
}

export class TransformRegistry {
  private readonly entries = new Map<string, Transform>();

  /**
   * Register a transform. Throws `DuplicateTransformError` on a
   * collision unless `options.replace` is true.
   */
  register(
    transform: Transform,
    options: RegisterTransformOptions = {},
  ): void {
    const existing = this.entries.get(transform.id);
    if (existing !== undefined && options.replace !== true) {
      throw new DuplicateTransformError(transform.id);
    }
    this.entries.set(transform.id, transform);
  }

  /** Look up a registered transform by id. `undefined` if not found. */
  get(id: string): Transform | undefined {
    return this.entries.get(id);
  }

  /** Snapshot list of every registered transform, in registration order. */
  list(): Transform[] {
    return [...this.entries.values()];
  }

  /** Returns true if a transform is registered under `id`. */
  has(id: string): boolean {
    return this.entries.has(id);
  }

  /**
   * Returns every transform whose source matches `blockName`. When a
   * concrete `block` is supplied, transforms that ship an `isMatch`
   * predicate get the chance to opt-out (e.g. "heading level up"
   * filters itself out for h1).
   */
  from(blockName: string, block?: Block): Transform[] {
    const matches: Transform[] = [];
    for (const t of this.entries.values()) {
      if (t.from !== blockName) continue;
      if (block !== undefined && t.isMatch !== undefined && !t.isMatch(block)) {
        continue;
      }
      matches.push(t);
    }
    return matches;
  }

  /** Returns every transform whose destination matches `blockName`. */
  to(blockName: string): Transform[] {
    const matches: Transform[] = [];
    for (const t of this.entries.values()) {
      if (t.to === blockName) matches.push(t);
    }
    return matches;
  }

  /**
   * Apply the transform identified by `id` to the given `block`. Throws
   * if the id is unknown; transforms that surface `isMatch` are still
   * applied (the host is expected to filter via `from()` first).
   *
   * Convenience over `registry.get(id)!.convert(block)`: in particular,
   * this is the call the editor's toolbar makes once the user picks a
   * dropdown item, so the registry owns the "unknown transform" error
   * message in one place.
   */
  apply(
    id: string,
    block: Block,
    context?: TransformContext,
  ): TransformResult {
    const transform = this.entries.get(id);
    if (transform === undefined) {
      throw new Error(`unknown transform: "${id}"`);
    }
    return transform.convert(block, context);
  }

  /**
   * Remove a registration. Returns true if something was removed.
   * Mostly useful in tests and HMR teardown.
   */
  unregister(id: string): boolean {
    return this.entries.delete(id);
  }

  /** Drop every registered transform. Test-only convenience. */
  clear(): void {
    this.entries.clear();
  }
}
