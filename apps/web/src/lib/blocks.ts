/**
 * Block tree → HTML walker for @gonext/web.
 *
 * Mirrors the Go-side `packages/go/blocks/render` walker so the editor
 * preview, the Go-side server render path, and this Next.js renderer
 * all produce byte-identical output for the same input.
 *
 * Strategy
 * ========
 * 1. Build a lookup from registered block name → `serverRender(attrs,
 *    innerHtml)`. The lookup is seeded from `CORE_BLOCKS` (every block
 *    @gonext/blocks-core ships) so every install has the core set out
 *    of the box. Plugin blocks register at boot via `registerBlock`.
 * 2. Walk the tree depth-first. Render each leaf into its HTML, then
 *    join sibling output before handing it to the parent's
 *    serverRender so containers receive a single `innerHtml` string.
 * 3. Unknown blocks render to a documented comment marker so an admin
 *    inspecting page source can see what got skipped, but the visitor
 *    sees nothing surprising. This matches the Go walker's behaviour.
 *
 * The walker is pure: same input → same bytes, no IO, no globals
 * touched outside the block-handler registry.
 */

import type { Block, BlockAttributes } from '@gonext/blocks-sdk';
// `@gonext/blocks-core/server` is the SSR-only sub-entry that ships
// just the pure `serverRender` hints (no React, no editor surface).
// Importing the main barrel here would pull in the Edit components,
// each of which transitively loads React hooks without `'use client'`
// and breaks the Next.js build.
import { CORE_SERVER_RENDERERS } from '@gonext/blocks-core/server';

/**
 * Signature of a server-side block renderer. Mirrors the
 * `CoreBlock.serverRender` shape used by every block in
 * @gonext/blocks-core.
 */
export type BlockServerRenderer<A extends BlockAttributes = BlockAttributes> =
  (attrs: A, innerHtml: string) => string;

/**
 * The handler registry. Map keyed by namespaced block name
 * (`"core/paragraph"`, `"plugin-slug/widget"`). Module-level so the
 * route handler doesn't re-seed every request — the cost of seeding
 * is small but it's still a needless allocation for a hot path.
 */
const handlers = new Map<string, BlockServerRenderer>();

function seedCoreHandlers(): void {
  if (handlers.size > 0) return;
  for (const [name, render] of CORE_SERVER_RENDERERS) {
    handlers.set(name, render as BlockServerRenderer);
  }
}

// Seed eagerly at module load. The cost is one-off and bounded by the
// 16 core blocks.
seedCoreHandlers();

/**
 * Register a plugin block's server-render hint. Plugins call this at
 * boot time (typically from a server-side `register-blocks` entry).
 *
 * Duplicate names overwrite by default — this matches the editor
 * registry's `replace: true` shape, which is what HMR / hot-reload of
 * a plugin block during development needs. Production plugin code is
 * loaded once at boot so collisions there indicate a packaging bug.
 */
export function registerBlock(
  name: string,
  render: BlockServerRenderer,
): void {
  handlers.set(name, render);
}

/**
 * Look up a handler. Exposed for tests; production callers should
 * stick to `renderBlocks`.
 */
export function getRegisteredHandler(
  name: string,
): BlockServerRenderer | undefined {
  return handlers.get(name);
}

/**
 * Marker for blocks we can't render. Visible only in raw HTML;
 * doesn't break layout. Used so an admin inspecting the page source
 * can grep for missing block types instead of staring at a blank
 * spot.
 */
function unknownBlockComment(name: string): string {
  // Block names are namespaced and slug-ish; we still sanitise to
  // strip anything that could close the comment early.
  const safe = name.replace(/-->|<!--/g, '');
  return `<!-- gn:unknown-block name="${safe}" -->`;
}

/**
 * Walk one block. Recurses into innerBlocks first so the container's
 * serverRender receives the already-rendered children — matches the
 * sentinel-substitution contract documented on @gonext/blocks-core's
 * container blocks (group, columns).
 */
function renderBlock(block: Block): string {
  if (!block || typeof block !== 'object') return '';
  // Defensive on the persisted shape: `type` is a required field, but
  // a runtime-corrupted tree (truncated migration, manually-edited
  // JSON) shouldn't crash the entire page render.
  const type = typeof block.type === 'string' ? block.type : '';
  if (!type) return '';

  const handler = handlers.get(type);

  const children = Array.isArray(block.innerBlocks)
    ? block.innerBlocks
    : [];
  const innerHtml = children.length > 0 ? renderBlocks(children) : '';

  if (!handler) {
    // Unknown block — emit the marker, but keep going so neighbours
    // still render. Drop the rendered children in line behind the
    // marker so authored content isn't silently lost on a missing
    // container block (e.g. a plugin uninstalled with content still
    // wrapped in its container).
    return unknownBlockComment(type) + innerHtml;
  }

  const attrs = (block.attributes ?? {}) as BlockAttributes;
  return handler(attrs, innerHtml);
}

/**
 * Walk a forest of root blocks. Sibling output is concatenated
 * without a separator — themes that want spacing add it via the block
 * wrapper itself (paragraphs, headings, group containers).
 */
export function renderBlocks(tree: readonly Block[] | undefined | null): string {
  if (!tree || tree.length === 0) return '';
  let out = '';
  for (const block of tree) {
    out += renderBlock(block);
  }
  return out;
}
