/**
 * Canonical TypeScript types for a block transform.
 *
 * A **transform** is a named "change block type" affordance: given a
 * source block (e.g. `core/paragraph`), produce one or more target
 * blocks (e.g. a `core/heading` carrying the same content). Transforms
 * power the toolbar's "Transform to..." dropdown in the editor and are
 * the public seam plugins use to teach the editor how to convert
 * between their own block types.
 *
 * The transform contract is intentionally narrow:
 *
 *  - **Pure** — `convert(block)` must not mutate its input. The host
 *    treats the return value as the new tree slice.
 *  - **Synchronous** — no Promises, no IO. A transform is the kind of
 *    operation users expect to apply in a single tick of the keyboard.
 *  - **Single source, single target type** — a `Transform` is keyed by
 *    `(from, to)`. A transform that fans out (e.g. list → paragraphs)
 *    still has a single `to` (`core/paragraph`); the `convert` return
 *    value is an array of blocks.
 *
 * Apart from `from` and `to`, every transform carries a stable `id`.
 * The id is namespaced (e.g. `core/paragraph-to-heading`) so plugins
 * can register transforms that supplement the built-ins without
 * collisions.
 */
import type { Block } from '@gonext/blocks-sdk';

/**
 * Result of applying a transform to a single source block. A transform
 * may return either a single replacement block (the common case for
 * paragraph→heading) or an array (e.g. list→paragraphs fans one block
 * out into N siblings). The host normalises the array case before
 * splicing the result into the parent tree.
 */
export type TransformResult = Block | Block[];

/**
 * Optional per-call context the host can pass into a transform. The
 * shape is intentionally open — built-in transforms only read a small
 * set of well-known keys (currently `columns` for the group → columns
 * conversion), but plugin transforms are free to consume anything the
 * editor injects.
 */
export interface TransformContext {
  /**
   * Number of columns to produce for transforms that need one (e.g.
   * group → columns). The editor prompts for this when the user picks
   * the transform and forwards the result here. Built-in transforms
   * default to `2` when omitted; plugin transforms may pick their own
   * sensible default.
   */
  columns?: number;
  /**
   * Whether the source content should be escaped before being placed
   * into the target (mainly: code → paragraph escapes HTML so a copy
   * of `<script>` in the code block doesn't suddenly render). Defaults
   * to `true` for the built-in code transforms; flip to `false` to
   * preserve the source bytes verbatim.
   */
  escapeHtml?: boolean;
  /**
   * Arbitrary plugin-supplied extras. Built-in transforms ignore this
   * — it exists so the registry's `apply()` overload stays open.
   */
  extra?: Record<string, unknown>;
}

/**
 * A single transform record.
 *
 *  - **`id`** — stable, namespaced identifier. Plugins should namespace
 *    with their slug, e.g. `my-plugin/widget-to-paragraph`.
 *  - **`from`** — source block name (`core/paragraph`, …).
 *  - **`to`** — destination block name. May equal `from` for "rotate
 *    inside the same type" transforms (heading level shifts).
 *  - **`label`** — short human label rendered in the dropdown.
 *  - **`description`** — optional one-line tooltip / aria-description.
 *  - **`convert`** — pure function from input block to result. See
 *    `TransformResult` for the fan-out contract.
 *  - **`isMatch`** — optional predicate: when present, the registry
 *    only surfaces this transform for blocks the predicate accepts.
 *    Useful for "heading level 6 → 5" which should not appear when the
 *    source already sits at level 6.
 */
export interface Transform {
  id: string;
  from: string;
  to: string;
  label: string;
  description?: string;
  convert: (block: Block, context?: TransformContext) => TransformResult;
  isMatch?: (block: Block) => boolean;
}
