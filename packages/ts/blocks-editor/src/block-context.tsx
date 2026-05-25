/**
 * Block context — the WordPress-style provide / consume channel that lets
 * a parent block expose state (e.g. `postId`, `postType`, `queryId`) to
 * its descendants without going through prop-drilling or a global store.
 *
 * The model is intentionally tiny:
 *
 *  - A block type's `BlockTypeDefinition.providesContext` lists the keys
 *    it wants to expose. When the editor walks the tree, it asks the
 *    block instance for those values (today: read straight off its
 *    `attributes`) and pushes them down via this React Context.
 *
 *  - A block type's `BlockTypeDefinition.usesContext` lists the keys it
 *    wants to read. Its Edit component receives the resolved values on
 *    the `context` prop (a `Record<string, unknown>` that
 *    `<BlockEditCanvas>` already passes through).
 *
 *  - Children can also reach the resolved map directly via
 *    `useBlockContext(key)` — the same hook the server-side walker's
 *    Go-side `Context` mirrors. Components in `@gonext/blocks-core`
 *    don't need this hook because the canvas threads `context` in as a
 *    prop already; the hook is the escape hatch for plugin authors who
 *    need to reach context from deep inside their own component tree.
 *
 * The provider stacks: a parent block's `providesContext` values are
 * merged into whatever ancestor context already existed, so the Query
 * block (which provides `postId` once per iteration) layers on top of
 * a `postType` from the root document context without clobbering it.
 *
 * Server parity: this is the same shape the Go walker's `Context` map
 * carries, so a block's `usesContext` keys read identically in the
 * editor preview and in the public page render. See
 * `packages/go/blocks/render` for the server-side counterpart.
 */
'use client';

import type { Block, BlockTypeDefinition } from '@gonext/blocks-sdk';
import {
  createContext,
  useContext,
  useMemo,
  type ReactNode,
} from 'react';

/**
 * Resolved context values keyed by name. Values are unknown at this
 * layer because each block type narrows its own `providesContext`
 * keys; the consuming block type's spec is what asserts the shape.
 */
export type BlockContextMap = Readonly<Record<string, unknown>>;

const EMPTY_CONTEXT: BlockContextMap = Object.freeze({});

const ReactBlockContext = createContext<BlockContextMap>(EMPTY_CONTEXT);
ReactBlockContext.displayName = 'GoNextBlockContext';

/**
 * Provider that merges `values` into the inherited block context for
 * everything rendered inside `children`. The merge is shallow — keys
 * in `values` win over the inherited ones.
 *
 * The `values` reference can change between renders without forcing a
 * re-render of the inherited context: the provider memoises the merged
 * map on the values' own identity. Callers that want a stable
 * reference should pass a stable object (e.g. `useMemo` on the parent
 * side); the provider itself is forgiving when `values` is a fresh
 * literal each render because the merged result is recomputed only
 * when either the inherited map or the new entries' identity change.
 */
export interface BlockContextProviderProps {
  /** New values to layer on top of the inherited context. */
  values: BlockContextMap;
  /** Children rendered with the merged context. */
  children: ReactNode;
}

export function BlockContextProvider({
  values,
  children,
}: BlockContextProviderProps): ReactNode {
  const inherited = useContext(ReactBlockContext);
  const merged = useMemo<BlockContextMap>(() => {
    // Avoid allocating a new object when there's nothing to add — the
    // identity stays stable so descendants don't see a spurious change.
    const keys = Object.keys(values);
    if (keys.length === 0) {
      return inherited;
    }
    return Object.freeze({ ...inherited, ...values });
  }, [inherited, values]);
  return (
    <ReactBlockContext.Provider value={merged}>
      {children}
    </ReactBlockContext.Provider>
  );
}

/**
 * Read a single key out of the inherited block context.
 *
 * Returns `undefined` when the key isn't provided by any ancestor.
 * Callers must defend against that case — a missing context entry is
 * not a programmer error; it's the natural state when a block is
 * dragged out from under its provider (e.g. a Query Loop child
 * dropped at the document root).
 *
 * Generic so plugin authors can narrow the return at the call site:
 *
 *   const postId = useBlockContext<string>('postId');
 */
export function useBlockContext<T = unknown>(key: string): T | undefined {
  const ctx = useContext(ReactBlockContext);
  return ctx[key] as T | undefined;
}

/**
 * Read the entire inherited block context map. Useful for plugin
 * components that want to forward the whole map further down (e.g.
 * into a non-block React subtree) or that branch on the presence of
 * multiple keys at once.
 */
export function useBlockContextMap(): BlockContextMap {
  return useContext(ReactBlockContext);
}

/**
 * Resolve the `providesContext` values for a given block instance.
 *
 * Today the contract is the simplest one that works: a block's
 * `providesContext` is a list of attribute names, and the resolved
 * value for each name is the attribute under that key. Plugin authors
 * who need synthesised context (e.g. a value derived from multiple
 * attributes) can pre-compute it onto a hidden attribute, or — once
 * the editor's selection model lands — register a custom resolver.
 *
 * The function never throws on missing keys: a `providesContext` key
 * that isn't on the block's attributes is dropped from the resolved
 * map. Descendants then see `undefined` from `useBlockContext`,
 * matching the natural "no value provided" state above.
 *
 * Exposed so the editor canvas and the Go walker's TS counterpart
 * (test fixtures, server-side preview tooling) share one resolution
 * rule.
 */
export function resolveProvidedContext(
  block: Block,
  definition: Pick<BlockTypeDefinition, 'providesContext'> | undefined,
): BlockContextMap {
  const keys = definition?.providesContext;
  if (keys === undefined || keys.length === 0) {
    return EMPTY_CONTEXT;
  }
  const out: Record<string, unknown> = {};
  const attrs = block.attributes as Record<string, unknown>;
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(attrs, key)) {
      out[key] = attrs[key];
    }
  }
  return Object.freeze(out);
}

/**
 * Filter an inherited context map down to the keys a block's
 * `usesContext` declares it wants. The canvas threads the filtered map
 * through as the `context` prop on `BlockEditProps`, so blocks that
 * don't opt in see an empty `{}` rather than the full ancestor map —
 * mirrors the WordPress Gutenberg "consumer opt-in" contract.
 *
 * Returns the same empty frozen reference when the consumer list is
 * empty so React.memo descendants don't see a churning prop.
 */
export function filterConsumedContext(
  ctx: BlockContextMap,
  definition: Pick<BlockTypeDefinition, 'usesContext'> | undefined,
): BlockContextMap {
  const keys = definition?.usesContext;
  if (keys === undefined || keys.length === 0) {
    return EMPTY_CONTEXT;
  }
  const out: Record<string, unknown> = {};
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(ctx, key)) {
      out[key] = ctx[key];
    }
  }
  return Object.freeze(out);
}

/**
 * The frozen empty context map. Exposed so callers (tests, the canvas's
 * root, the Go walker's TS bridge) can compare against the same
 * identity rather than allocating their own `{}`.
 */
export const EMPTY_BLOCK_CONTEXT = EMPTY_CONTEXT;
