/**
 * @gonext/blocks-core — public entry point.
 *
 * Ships the sixteen **core** blocks every GoNext install relies on:
 *
 *  - `core/paragraph` — plain narrative text
 *  - `core/heading`   — h1..h6 with optional anchor
 *  - `core/list`      — ordered / unordered list
 *  - `core/image`     — figure / image / caption / link
 *  - `core/quote`     — blockquote with citation
 *  - `core/code`      — pre / code with optional language hint
 *  - `core/separator` — visual `<hr/>` divider
 *  - `core/spacer`    — vertical whitespace
 *  - `core/columns`   — N-column layout container
 *  - `core/group`     — generic wrapper container
 *  - `core/table`     — tabular data with optional thead/tfoot/caption
 *  - `core/gallery`   — image grid with reorder + crop
 *  - `core/video`     — `<figure><video/></figure>` with playback flags
 *  - `core/button`    — call-to-action link styled as a button
 *  - `core/file`      — downloadable file link + button
 *  - `core/embed`     — provider-aware oEmbed wrapper
 *
 * Every block exposes:
 *  - **`definition`** — `BlockTypeDefinition` for the registry
 *  - **`Edit`**       — React component used in the editor canvas
 *  - **`save`**       — pure serializer to canonical HTML
 *  - **`serverRender`** — server-render hint mirroring the Go template;
 *    accepts `(attrs, innerHtml)` so container blocks can splice in the
 *    already-rendered children produced by the walker.
 *
 * Most consumers will only ever call `registerCoreBlocks(registry)`. The
 * individual block exports are there for cases where an app wants to swap
 * one out (e.g. a custom paragraph) but keep the rest.
 */

import type { BlockRegistry, BlockTypeDefinition } from '@gonext/blocks-sdk';

import { paragraph } from './paragraph/index.ts';
import { heading } from './heading/index.ts';
import { list } from './list/index.ts';
import { image } from './image/index.ts';
import { quote } from './quote/index.ts';
import { code } from './code/index.ts';
import { separator } from './separator/index.ts';
import { spacer } from './spacer/index.ts';
import { columns } from './columns/index.ts';
import { group } from './group/index.ts';
import { table } from './table/index.ts';
import { gallery } from './gallery/index.ts';
import { video } from './video/index.ts';
import { button } from './button/index.ts';
import { file } from './file/index.ts';
import { embed } from './embed/index.ts';

// Per-block re-exports so consumers can `import { paragraph } from
// '@gonext/blocks-core'` and reach into `paragraph.definition`,
// `paragraph.save`, `paragraph.serverRender` as needed.
export { paragraph } from './paragraph/index.ts';
export { heading } from './heading/index.ts';
export { list } from './list/index.ts';
export { image } from './image/index.ts';
export { quote } from './quote/index.ts';
export { code } from './code/index.ts';
export { separator } from './separator/index.ts';
export { spacer } from './spacer/index.ts';
export { columns } from './columns/index.ts';
export { group } from './group/index.ts';
export { table } from './table/index.ts';
export { gallery } from './gallery/index.ts';
export { video } from './video/index.ts';
export { button } from './button/index.ts';
export { file } from './file/index.ts';
export { embed } from './embed/index.ts';

// Edit components are re-exported so app code that wants to mount a single
// block in isolation (e.g. a focused review surface) can do so without
// pulling the entire registry path.
export { ParagraphEdit } from './paragraph/index.ts';
export { HeadingEdit } from './heading/index.ts';
export { ListEdit } from './list/index.ts';
export { ImageEdit } from './image/index.ts';
export { QuoteEdit } from './quote/index.ts';
export { CodeEdit } from './code/index.ts';
export { SeparatorEdit } from './separator/index.ts';
export { SpacerEdit } from './spacer/index.ts';
export { ColumnsEdit } from './columns/index.ts';
export { GroupEdit } from './group/index.ts';
export { TableEdit } from './table/index.ts';
export { GalleryEdit } from './gallery/index.ts';
export { VideoEdit } from './video/index.ts';
export { ButtonEdit } from './button/index.ts';
export { FileEdit } from './file/index.ts';
export { EmbedEdit } from './embed/index.ts';

// Per-block attribute types.
export type { ParagraphAttributes } from './paragraph/index.ts';
export type { HeadingAttributes } from './heading/index.ts';
export type { ListAttributes } from './list/index.ts';
export type { ImageAttributes } from './image/index.ts';
export type { QuoteAttributes } from './quote/index.ts';
export type { CodeAttributes } from './code/index.ts';
export type { SeparatorAttributes } from './separator/index.ts';
export type { SpacerAttributes } from './spacer/index.ts';
export type { ColumnsAttributes } from './columns/index.ts';
export type { GroupAttributes } from './group/index.ts';
export type { TableAttributes, TableRow } from './table/index.ts';
export type { GalleryAttributes, GalleryImage } from './gallery/index.ts';
export type { VideoAttributes } from './video/index.ts';
export type { ButtonAttributes } from './button/index.ts';
export type { FileAttributes } from './file/index.ts';
export type { EmbedAttributes, EmbedProvider } from './embed/index.ts';

// Embed provider detection — exposed so the editor's URL-paste handler
// can compute the slug before persisting it on the block.
export { detectProvider, EMBED_PROVIDERS } from './embed/index.ts';

// Container-block inner-HTML sentinels — exposed so the Go walker's TS
// counterpart (the editor's full save pipeline) knows where to splice
// rendered children into the static save() output.
export { COLUMNS_INNER_SENTINEL } from './columns/index.ts';
export { GROUP_INNER_SENTINEL } from './group/index.ts';

/**
 * The complete ordered list of every core block, in the order they appear
 * in the inserter. Plugin code can iterate this list to inspect supports
 * matrices, run cross-block tests, etc.
 */
export const CORE_BLOCKS = [
  paragraph,
  heading,
  list,
  image,
  quote,
  code,
  separator,
  spacer,
  columns,
  group,
  table,
  gallery,
  video,
  button,
  file,
  embed,
] as const;

/**
 * Register every core block on a given `BlockRegistry`. Matches the
 * `defaultCoreBlocks(...)` shape exposed by `@gonext/blocks-editor` so
 * apps that already wired the latter can swap in the full set with a
 * one-line change.
 *
 * Pass `{ replace: true }` only for HMR-style reloads — production code
 * should leave it off so a duplicate registration throws loudly.
 */
export function registerCoreBlocks(
  registry: BlockRegistry,
  options: { replace?: boolean } = {},
): void {
  for (const block of CORE_BLOCKS) {
    // Widen to the base BlockTypeDefinition: the heterogeneous tuple's
    // per-entry attribute generic would otherwise be inferred as the
    // first entry's shape, which is wrong for the rest.
    registry.register(block.definition as BlockTypeDefinition, options);
  }
}
