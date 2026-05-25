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
 *
 * Visual styling — "Living systems" brand (docs/design/HANDOFF.md):
 *   - Canvas sits on cream `--paper` with generous editorial padding so the
 *     document chip floats; the chip itself is `--paper-2` framed with a
 *     hairline `--border` and a soft `--sh-md` drop shadow.
 *   - The selected block carries an emerald left-rule, mirroring the
 *     "Selected block has emerald left-border" note in the editor mock.
 *   - All colour, spacing, type and shadow values are expressed as CSS
 *     custom-property references — tokens are law (see
 *     docs/design/colors_and_type.css). Values gracefully fall back to
 *     literal hex/px when the admin tokens.css isn't loaded (vitest
 *     snapshots, dev sandboxes), so the package stays useful in isolation.
 */
'use client';

import type {
  Block,
  BlockEditProps,
  BlockRegistry,
  BlockTree,
  EditComponent,
} from '@gonext/blocks-sdk';
import { createElement, Suspense, use, type CSSProperties } from 'react';
import {
  BlockContextProvider,
  filterConsumedContext,
  resolveProvidedContext,
  useBlockContextMap,
  type BlockContextMap,
} from './block-context.tsx';
import { BlockTransformToolbar } from './block-transform-toolbar.tsx';
import type {
  Transform,
  TransformRegistry,
} from './transform-types.ts';

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
  /**
   * Optional transform registry. When passed, each block in the tree
   * renders a "Transform to..." dropdown next to it in the canvas
   * toolbar; clicking an option calls `onApplyTransform` with the
   * source block + chosen transform id. When omitted, the toolbar is
   * not rendered — the canvas degrades back to its plain walker shape.
   */
  transformRegistry?: TransformRegistry;
  /**
   * Called when an author picks a transform from the toolbar. The
   * host is expected to apply the transform via the editor's
   * mutation API (look up the transform on the registry, call its
   * `convert`, splice the result into the tree). Required when
   * `transformRegistry` is set; otherwise unused.
   */
  onApplyTransform?: (
    block: Block,
    transformId: string,
    transform: Transform,
  ) => void;
  /**
   * `clientId` of the currently selected block, if any. The canvas
   * adds an emerald left-rule + indent to the matching node so
   * authors can see what they're editing. Optional — when omitted
   * nothing is selected and the canvas renders its plain "walker"
   * shape exactly as before.
   */
  selectedClientId?: string;
}

/**
 * Style tokens for the canvas. Tokens-as-law: every value below references
 * a CSS custom property declared in `apps/admin/src/styles/tokens.css`,
 * with a literal fallback so the package stays renderable in isolation
 * (e.g. vitest snapshots that don't pull in the admin stylesheet).
 */
const canvasStyle: CSSProperties = {
  background: 'var(--paper, #F5F2EA)',
  padding: '40px 56px 200px',
  minHeight: '100%',
  fontFamily:
    "var(--font-sans, 'Geist', -apple-system, system-ui, sans-serif)",
  color: 'var(--ink-soft, #1F2D26)',
};

const docChipStyle: CSSProperties = {
  maxWidth: 720,
  margin: '0 auto',
  background: 'var(--paper-2, #EFEBE0)',
  border: '1px solid var(--border, #D9D2C0)',
  borderRadius: 'var(--r-lg, 12px)',
  padding: 'var(--s-7, 32px) var(--s-7, 32px) var(--s-8, 48px)',
  boxShadow:
    'var(--sh-md, 0 6px 14px -4px rgba(14, 26, 20, 0.08), 0 2px 6px -2px rgba(14, 26, 20, 0.04))',
};

const nodeStyle: CSSProperties = {
  position: 'relative',
  padding: '4px 0',
  transition:
    'box-shadow var(--dur, 160ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1)), padding var(--dur, 160ms) var(--ease, cubic-bezier(0.2, 0.7, 0.2, 1))',
};

const selectedNodeStyle: CSSProperties = {
  ...nodeStyle,
  boxShadow: '-3px 0 0 var(--emerald, #10B981)',
  paddingLeft: 14,
  marginLeft: -14,
};

const childrenStyle: CSSProperties = {
  marginTop: 'var(--s-2, 8px)',
  paddingLeft: 'var(--s-4, 16px)',
  borderLeft: '1px dashed var(--border-subtle, #E8E2D1)',
};

const unknownStyle: CSSProperties = {
  padding: 'var(--s-3, 12px) var(--s-4, 16px)',
  border: '1px dashed var(--danger, #DC2626)',
  borderRadius: 'var(--r-md, 8px)',
  background: 'var(--danger-soft, #FEE2E2)',
  color: 'var(--danger, #DC2626)',
  fontFamily:
    "var(--font-mono, 'Geist Mono', ui-monospace, monospace)",
  fontSize: 'var(--t-sm, 13px)',
};

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
  transformRegistry,
  onApplyTransform,
  selectedClientId,
}: BlockEditCanvasProps) {
  // Empty trees render as a bare canvas root — the document chip only
  // materialises when there is something to author. This keeps the
  // "empty editor === empty DOM" contract the scaffold tests assert.
  if (blocks.length === 0) {
    return (
      <div
        className="gonext-block-edit-canvas"
        data-testid="block-edit-canvas"
      />
    );
  }
  // Seed the block-context tree with whatever the host passed via
  // `context`. Inside the tree, container blocks layer their
  // `providesContext` values on top via `BlockContextProvider` (see
  // BlockNode below). The `context` prop on `BlockEditProps` is
  // computed per-block from the inherited map filtered through that
  // block's `usesContext` declaration — so an Edit component that
  // doesn't opt into context still sees `{}`, matching the WordPress
  // Gutenberg consumer-opt-in contract.
  const rootContext = (context ?? {}) as BlockContextMap;
  return (
    <div
      className="gonext-block-edit-canvas"
      data-testid="block-edit-canvas"
      style={canvasStyle}
    >
      <div
        className="gonext-block-edit-canvas__doc"
        data-testid="block-edit-canvas-doc"
        style={docChipStyle}
      >
        <Suspense fallback={loadingFallback}>
          <BlockContextProvider values={rootContext}>
            {blocks.map((block, index) => (
              <BlockNode
                key={block.clientId ?? `${block.type}-${index}`}
                block={block}
                registry={registry}
                indexPath={[index]}
                transformRegistry={transformRegistry}
                onApplyTransform={onApplyTransform}
                selectedClientId={selectedClientId}
              />
            ))}
          </BlockContextProvider>
        </Suspense>
      </div>
    </div>
  );
}

interface BlockNodeProps {
  block: Block;
  registry: BlockRegistry;
  indexPath: number[];
  transformRegistry?: TransformRegistry;
  onApplyTransform?: (
    block: Block,
    transformId: string,
    transform: Transform,
  ) => void;
  selectedClientId?: string;
}

function BlockNode({
  block,
  registry,
  indexPath,
  transformRegistry,
  onApplyTransform,
  selectedClientId,
}: BlockNodeProps): React.ReactNode {
  // Read the inherited block context here so any ancestor's
  // `providesContext` values surface to this node. The Edit
  // component receives only the subset it opts into via
  // `usesContext`; children rendered below see the merged map.
  const inheritedContext = useBlockContextMap();

  const def = registry.get(block.type);
  if (def === undefined) {
    return <UnknownBlock type={block.type} path={indexPath} />;
  }

  // React 19's `use()` will suspend until the promise resolves. The cache
  // ensures we only kick off one import per (registry, blockType) pair.
  const mod = use(getEditModule(registry, block.type, def.edit));
  const Edit = mod.default as EditComponent;

  const resolvedClientId =
    block.clientId ?? `${block.type}-${indexPath.join('-')}`;
  const isSelected =
    selectedClientId !== undefined && selectedClientId === resolvedClientId;

  // Per-block consumed context. A block that lists no `usesContext`
  // gets the shared empty frozen reference — React.memo descendants
  // won't see a churning prop on every render.
  const consumed = filterConsumedContext(inheritedContext, def);

  const props: BlockEditProps = {
    attributes: block.attributes,
    setAttributes: noopSetAttributes,
    isSelected,
    clientId: resolvedClientId,
    context: consumed as Record<string, unknown>,
  };

  // Use `createElement` so React owns the mount lifecycle. Calling `Edit`
  // as a plain function would bypass hooks + Suspense in the edit
  // component itself — fine today (placeholders), wrong tomorrow.
  const rendered = createElement(
    Edit as unknown as React.ComponentType<BlockEditProps>,
    props,
  );

  // The toolbar only renders when a host wires a transform registry in.
  // When omitted, the canvas degrades back to its original "walker only"
  // shape — preserving the test contract from before this issue.
  const showToolbar =
    transformRegistry !== undefined && onApplyTransform !== undefined;

  // Resolve the values this block exposes to its descendants. When
  // there's nothing to provide we skip the provider wrapper entirely
  // so the React tree stays shallow for the common (leaf) case.
  const provided = resolveProvidedContext(block, def);
  const hasChildren =
    block.innerBlocks !== undefined && block.innerBlocks.length > 0;

  const childrenNode = hasChildren ? (
    <div
      className="gonext-block-edit-canvas__children"
      style={childrenStyle}
    >
      {block.innerBlocks!.map((child, childIndex) => (
        <BlockNode
          key={child.clientId ?? `${child.type}-${childIndex}`}
          block={child}
          registry={registry}
          indexPath={[...indexPath, childIndex]}
          transformRegistry={transformRegistry}
          onApplyTransform={onApplyTransform}
          selectedClientId={selectedClientId}
        />
      ))}
    </div>
  ) : null;

  const wrappedChildren =
    hasChildren && Object.keys(provided).length > 0 ? (
      <BlockContextProvider values={provided}>{childrenNode}</BlockContextProvider>
    ) : (
      childrenNode
    );

  return (
    <div
      className={
        'gonext-block-edit-canvas__node' +
        (isSelected ? ' gonext-block-edit-canvas__node--selected' : '')
      }
      data-block-type={block.type}
      data-selected={isSelected ? 'true' : 'false'}
      data-testid={`block-edit-canvas-node-${block.type}`}
      style={isSelected ? selectedNodeStyle : nodeStyle}
    >
      {showToolbar ? (
        <BlockTransformToolbar
          block={block}
          registry={transformRegistry}
          onApply={(transformId, transform) =>
            onApplyTransform(block, transformId, transform)
          }
        />
      ) : null}
      {rendered}
      {wrappedChildren}
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
      style={unknownStyle}
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
