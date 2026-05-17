/**
 * `<BlockEditCanvas>` — placeholder shell that walks a block tree and renders
 * each block's lazy-imported `edit` component.
 *
 * This is the seed of the real editor surface. The contract here is
 * intentionally small:
 *
 *  - Take a `BlockTree` and a `BlockRegistry`.
 *  - For every block, look up its `BlockTypeDefinition`, lazy-import its
 *    `edit` component, and render it with a synthetic `BlockEditProps`
 *    (no real selection model — that lands later).
 *  - Unknown block types fall back to a `<UnknownBlock>` placeholder so the
 *    rest of the tree keeps rendering. Loud-but-non-fatal matches the
 *    Gutenberg precedent.
 *
 * The dynamic-import flow uses React 19's `use()` plus `Suspense`. Each tile
 * shares one cached promise per (registry, blockType) pair so re-renders
 * don't kick off a thundering herd of imports. The cache is keyed by the
 * registry reference so swapping registries (e.g. test isolation) doesn't
 * leak between trees.
 */
'use client';

import type {
  Block,
  BlockEditProps,
  BlockRegistry,
  BlockTree,
  EditComponent,
} from '@gonext/blocks-sdk';
import { createElement, Suspense, use } from 'react';

export interface BlockEditCanvasProps {
  /** Registry of block types. */
  registry: BlockRegistry;
  /** The tree to render. */
  blocks: BlockTree;
  /**
   * Extra context object handed down to every `edit` component. The shape is
   * intentionally open — the editor passes things like the current post id,
   * theme tokens, and feature flags through this channel.
   */
  context?: Record<string, unknown>;
  /**
   * Fallback rendered while an `edit` component is being lazy-loaded. The
   * SDK doesn't ship a designed loader; the editor app supplies one.
   */
  loadingFallback?: React.ReactNode;
}

/**
 * Walks `blocks` and renders each node. The rendering happens inside a
 * single `<Suspense>` boundary; any in-flight `edit()` import shows
 * `loadingFallback` until it settles.
 */
export function BlockEditCanvas({
  registry,
  blocks,
  context,
  loadingFallback = null,
}: BlockEditCanvasProps) {
  return (
    <div
      className="gonext-block-edit-canvas"
      data-testid="block-edit-canvas"
    >
      <Suspense fallback={loadingFallback}>
        {blocks.map((block, index) => (
          <BlockNode
            key={block.clientId ?? `${block.type}-${index}`}
            block={block}
            registry={registry}
            context={context ?? {}}
            indexPath={[index]}
          />
        ))}
      </Suspense>
    </div>
  );
}

interface BlockNodeProps {
  block: Block;
  registry: BlockRegistry;
  context: Record<string, unknown>;
  indexPath: number[];
}

function BlockNode({
  block,
  registry,
  context,
  indexPath,
}: BlockNodeProps): React.ReactNode {
  const def = registry.get(block.type);
  if (def === undefined) {
    return <UnknownBlock type={block.type} path={indexPath} />;
  }

  // React 19's `use()` will suspend until the promise resolves. The cache
  // ensures we only kick off one import per (registry, blockType) pair.
  const mod = use(getEditModule(registry, block.type, def.edit));
  const Edit = mod.default as EditComponent;

  const props: BlockEditProps = {
    attributes: block.attributes,
    setAttributes: noopSetAttributes,
    isSelected: false,
    clientId:
      block.clientId ?? `${block.type}-${indexPath.join('-')}`,
    context,
  };

  // Use `createElement` so React owns the mount lifecycle. Calling `Edit`
  // as a plain function would bypass hooks + Suspense in the edit
  // component itself — fine today (placeholders), wrong tomorrow.
  const rendered = createElement(
    Edit as unknown as React.ComponentType<BlockEditProps>,
    props,
  );

  return (
    <div
      className="gonext-block-edit-canvas__node"
      data-block-type={block.type}
      data-testid={`block-edit-canvas-node-${block.type}`}
    >
      {rendered}
      {block.innerBlocks && block.innerBlocks.length > 0 ? (
        <div className="gonext-block-edit-canvas__children">
          {block.innerBlocks.map((child, childIndex) => (
            <BlockNode
              key={child.clientId ?? `${child.type}-${childIndex}`}
              block={child}
              registry={registry}
              context={context}
              indexPath={[...indexPath, childIndex]}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

interface UnknownBlockProps {
  type: string;
  path: number[];
}

function UnknownBlock({ type, path }: UnknownBlockProps) {
  return (
    <div
      role="alert"
      className="gonext-block-edit-canvas__unknown"
      data-testid="block-edit-canvas-unknown"
      data-block-type={type}
      data-path={path.join('/')}
    >
      Unknown block type: <code>{type}</code>
    </div>
  );
}

/**
 * `setAttributes` is a no-op at this stage — the canvas is read-only for
 * the scaffold. Wiring it up to a state store happens in a later issue.
 */
function noopSetAttributes() {
  // intentionally empty
}

/**
 * Cache the resolved `edit()` import promise per registry instance. We have
 * to key by registry reference too, because a test swapping registries
 * should be able to re-register a block name with a different `edit`
 * implementation without bleeding through.
 */
type EditModule = { default: EditComponent };
const editModuleCache = new WeakMap<
  BlockRegistry,
  Map<string, Promise<EditModule>>
>();

function getEditModule(
  registry: BlockRegistry,
  blockType: string,
  factory: () => Promise<EditModule>,
): Promise<EditModule> {
  let perRegistry = editModuleCache.get(registry);
  if (perRegistry === undefined) {
    perRegistry = new Map();
    editModuleCache.set(registry, perRegistry);
  }
  let promise = perRegistry.get(blockType);
  if (promise === undefined) {
    promise = factory();
    perRegistry.set(blockType, promise);
  }
  return promise;
}

/**
 * Clear the cached lazy-imported `edit` modules for a specific registry.
 * Exposed for the editor's HMR path and for tests that re-register a block
 * name after a render has already cached its module.
 */
export function clearEditModuleCache(registry: BlockRegistry): void {
  editModuleCache.delete(registry);
}
