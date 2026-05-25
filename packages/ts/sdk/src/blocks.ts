/**
 * Client-side block registration.
 *
 * `defineBlock(spec)` is how a plugin's browser bundle registers a
 * block component the editor + the public theme can render. The
 * concrete registry — the global object the host writes blocks into
 * — is owned by the editor package (`@gonext/blocks-editor`) and
 * mounted on the page before plugin bundles evaluate.
 *
 * This module forwards to that registry when it exists, and
 * silently captures the registration into a local Map otherwise.
 * The capture is observable via `__getCapturedBlocks()` so tests
 * (and the editor, once it boots) can flush the pending list.
 *
 * Forward-compat: the `BLOCK_REGISTRY` shape will probably grow
 * lifecycle hooks (mount / unmount / capability gates). We pass
 * the full spec through verbatim — the SDK does not normalize or
 * validate beyond a name check — so a registry update doesn't
 * require an SDK rev.
 */

import type { ComponentType } from './react-types';

/**
 * Block-spec shape. Mirrors the contract the editor's
 * `BLOCK_REGISTRY.register` expects.
 *
 * `name` is the block identifier and follows the WP convention of
 * `<namespace>/<slug>` (e.g. `acme/quote`). The SDK enforces the
 * shape so a typo (`'quote'`) doesn't silently shadow another
 * plugin's block.
 *
 * `edit` is the editor-side React component; `save` is the public
 * theme's render component. Either or both may be omitted — server-
 * rendered blocks have neither, and edit-only blocks have no save.
 * The registry tolerates `undefined` for both.
 *
 * `attributes` is the block's attribute schema, in the same shape
 * Gutenberg's `registerBlockType` expects. The SDK does not validate
 * it; the editor performs full validation at registration time and
 * surfaces errors to the developer.
 */
export interface BlockSpec<Attrs = Record<string, unknown>> {
  name: string;
  title?: string;
  icon?: string;
  category?: string;
  description?: string;
  keywords?: ReadonlyArray<string>;
  attributes?: Attrs;
  edit?: ComponentType<BlockProps<Attrs>>;
  save?: ComponentType<BlockProps<Attrs>>;
  /** Free-form pass-through for future registry fields. */
  [extra: string]: unknown;
}

/**
 * Props the block's `edit` and `save` components receive. The
 * concrete shape is owned by the editor; we declare just the two
 * fields the SDK needs to expose for type-checking.
 */
export interface BlockProps<Attrs = Record<string, unknown>> {
  attributes: Attrs;
  setAttributes?: (next: Partial<Attrs>) => void;
}

/**
 * Shape of the global block registry the editor mounts. We declare
 * it via an interface and treat the global at call time as
 * potentially-undefined; the SDK is the upstream package, so it
 * cannot statically depend on the editor exporting one.
 */
interface BlockRegistry {
  register: (spec: BlockSpec<Record<string, unknown>>) => void;
  unregister?: (name: string) => void;
}

/**
 * Module-scoped capture for the "registry not present yet" path.
 * Keyed by block name so a re-registration replaces the previous
 * entry instead of stacking duplicates.
 */
const captured = new Map<string, BlockSpec<Record<string, unknown>>>();

/**
 * Block-name validation pattern. Lower-case namespace, slash,
 * kebab-case slug. Matches the WP block-type rules so a plugin
 * authored against live WP doesn't need to rename blocks.
 */
const BLOCK_NAME_PATTERN = /^[a-z][a-z0-9-]*\/[a-z][a-z0-9-]*$/;

/**
 * Registers a block. If the editor has already published a
 * `window.__GN_BLOCK_REGISTRY__` (or the equivalent
 * `globalThis.BLOCK_REGISTRY` constant the editor exposes), the
 * call forwards directly. Otherwise the spec is captured locally
 * and the editor will pull captured specs at boot.
 *
 * Throws `TypeError` for an invalid `name`. NOT thrown for missing
 * `edit` / `save` — server-rendered blocks have neither.
 */
export function defineBlock<Attrs extends Record<string, unknown>>(
  spec: BlockSpec<Attrs>,
): void {
  if (typeof spec.name !== 'string' || !BLOCK_NAME_PATTERN.test(spec.name)) {
    throw new TypeError(
      `[@gonext/sdk] defineBlock: name ${JSON.stringify(spec.name)} ` +
        'must be "<namespace>/<slug>" with lower-case ASCII.',
    );
  }
  // Cast Attrs → unknown for the registry surface. The block
  // registry is generic-erased; we recover the type at the call
  // sites where the component is actually rendered.
  const registry = getBlockRegistry();
  const erased = spec as unknown as BlockSpec<Record<string, unknown>>;
  if (registry !== null) {
    registry.register(erased);
    return;
  }
  captured.set(spec.name, erased);
}

/**
 * Reads the block registry off the global, if present. The editor
 * publishes the registry under the conventional name
 * `__GN_BLOCK_REGISTRY__`; we ALSO accept a `BLOCK_REGISTRY`
 * camel-cased export for the very-old editor builds that shipped
 * before the convention firmed up.
 */
function getBlockRegistry(): BlockRegistry | null {
  if (typeof globalThis === 'undefined') {
    return null;
  }
  const g = globalThis as {
    __GN_BLOCK_REGISTRY__?: unknown;
    BLOCK_REGISTRY?: unknown;
  };
  const candidate = g.__GN_BLOCK_REGISTRY__ ?? g.BLOCK_REGISTRY;
  if (candidate === undefined || candidate === null) {
    return null;
  }
  if (typeof (candidate as { register?: unknown }).register !== 'function') {
    return null;
  }
  return candidate as BlockRegistry;
}

/**
 * Drains captured specs. Called by the editor on boot once its
 * registry is ready, OR by tests to assert what was registered.
 *
 * Returns an array of specs in insertion order. Subsequent calls
 * see an empty list — drain is destructive on purpose so the
 * editor doesn't double-register.
 */
export function __drainCapturedBlocks(): Array<BlockSpec<Record<string, unknown>>> {
  const list = Array.from(captured.values());
  captured.clear();
  return list;
}

/**
 * Read-only peek into the captured map. Distinct from `__drain`
 * because tests often want to assert WITHOUT clearing.
 */
export function __getCapturedBlocks(): ReadonlyArray<BlockSpec<Record<string, unknown>>> {
  return Array.from(captured.values());
}
