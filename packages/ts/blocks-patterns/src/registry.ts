/**
 * `PatternRegistry` — the runtime map from pattern ids to their fixtures.
 *
 * Mirrors the design of `BlockRegistry` (single-write, throws on
 * collision unless `replace: true`) so plugin authors who already know
 * the block registry have one less surface to learn. The map is keyed
 * by `Pattern.id`; collisions surface as `DuplicatePatternError`.
 *
 * Validation is intentionally NOT performed in the registry itself —
 * patterns reference block types whose registry lives in a different
 * package, so coupling the two here would force every consumer to wire
 * up both. The `registerCorePatterns()` helper does the optional check
 * against a passed-in `BlockRegistry` when invoked from the editor.
 */
import type { Pattern } from './types.ts';
import type { PatternCategory } from './categories.ts';

export interface RegisterPatternOptions {
  /**
   * Allow overwriting an existing registration. Defaults to false. Mostly
   * useful in tests and the editor's HMR teardown.
   */
  replace?: boolean;
}

export class DuplicatePatternError extends Error {
  public override readonly name = 'DuplicatePatternError';
  public readonly patternId: string;

  constructor(patternId: string) {
    super(
      `pattern "${patternId}" is already registered. ` +
        `Pass { replace: true } to overwrite (HMR only).`,
    );
    this.patternId = patternId;
  }
}

export class PatternRegistry {
  private readonly entries = new Map<string, Pattern>();

  /**
   * Register a pattern. Throws `DuplicatePatternError` on a collision
   * unless `options.replace` is true.
   */
  register(
    pattern: Pattern,
    options: RegisterPatternOptions = {},
  ): void {
    const existing = this.entries.get(pattern.id);
    if (existing !== undefined && options.replace !== true) {
      throw new DuplicatePatternError(pattern.id);
    }
    this.entries.set(pattern.id, pattern);
  }

  /** Look up a registered pattern by id. `undefined` if not found. */
  get(id: string): Pattern | undefined {
    return this.entries.get(id);
  }

  /** Snapshot list of every registered pattern, in registration order. */
  list(): Pattern[] {
    return [...this.entries.values()];
  }

  /** Returns true if a pattern is registered under `id`. */
  has(id: string): boolean {
    return this.entries.has(id);
  }

  /**
   * Returns every pattern registered under the given `category`, in
   * registration order. Unknown categories return an empty array — the
   * inserter UI uses this to drive its "category has no patterns yet"
   * empty state.
   */
  byCategory(category: PatternCategory): Pattern[] {
    return this.list().filter((p) => p.category === category);
  }

  /**
   * Returns the sorted, de-duplicated list of categories that currently
   * have at least one registered pattern. The inserter's category tabs
   * intersect this with `BUILT_IN_PATTERN_CATEGORIES` to render only
   * tabs that have content, while still allowing plugin-defined
   * categories to appear at the end.
   */
  categories(): PatternCategory[] {
    const set = new Set<PatternCategory>();
    for (const p of this.entries.values()) set.add(p.category);
    return [...set];
  }

  /**
   * Remove a registration. Returns true if something was removed.
   * Mostly useful in tests and HMR teardown.
   */
  unregister(id: string): boolean {
    return this.entries.delete(id);
  }

  /** Drop every registered pattern. Test-only convenience. */
  clear(): void {
    this.entries.clear();
  }
}
