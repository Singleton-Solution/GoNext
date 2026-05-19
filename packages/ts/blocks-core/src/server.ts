/**
 * `@gonext/blocks-core/server` — SSR-only entry point.
 *
 * Exports the pure server-render hints (and `save` serialisers) for
 * every core block, with NO React surface alongside. The main barrel
 * (`@gonext/blocks-core`) re-exports the editor's `Edit` components,
 * each of which transitively imports React hooks; bundlers that walk
 * the main barrel under a server-component context (Next.js App
 * Router, the public site renderer) drag those hooks into the server
 * graph and the build fails.
 *
 * This module sidesteps that by exporting only the half of each
 * block's surface that lives in `save.ts`. Every function here is:
 *
 *   - pure (same input → same output)
 *   - synchronous
 *   - free of React, DOM, and any global state
 *
 * Consumers that need to walk a block tree on the server (the
 * `apps/web` public renderer, the Go-side render walker's TS test
 * fixtures, server-side preview tooling) import from here:
 *
 *     import { CORE_SERVER_RENDERERS } from '@gonext/blocks-core/server';
 *
 * The map shape is identical to what `@gonext/blocks-core` would
 * surface internally — a tuple of `[blockName, serverRender]` pairs
 * the consumer registers on their walker's handler table.
 *
 * Plugin authors who ship server-side rendering should follow the
 * same pattern: keep a `save.ts` / `server.ts` boundary so SSR
 * consumers never have to pull in the editor surface.
 */

import type { BlockAttributes } from '@gonext/blocks-sdk';

import {
  save as paragraphSave,
  serverRender as paragraphServerRender,
} from './paragraph/save.ts';
import {
  save as headingSave,
  serverRender as headingServerRender,
} from './heading/save.ts';
import {
  save as listSave,
  serverRender as listServerRender,
} from './list/save.ts';
import {
  save as imageSave,
  serverRender as imageServerRender,
} from './image/save.ts';
import {
  save as quoteSave,
  serverRender as quoteServerRender,
} from './quote/save.ts';
import {
  save as codeSave,
  serverRender as codeServerRender,
} from './code/save.ts';
import {
  save as separatorSave,
  serverRender as separatorServerRender,
} from './separator/save.ts';
import {
  save as spacerSave,
  serverRender as spacerServerRender,
} from './spacer/save.ts';
import {
  save as columnsSave,
  serverRender as columnsServerRender,
} from './columns/save.ts';
import {
  save as groupSave,
  serverRender as groupServerRender,
} from './group/save.ts';
import {
  save as tableSave,
  serverRender as tableServerRender,
} from './table/save.ts';
import {
  save as gallerySave,
  serverRender as galleryServerRender,
} from './gallery/save.ts';
import {
  save as videoSave,
  serverRender as videoServerRender,
} from './video/save.ts';
import {
  save as buttonSave,
  serverRender as buttonServerRender,
} from './button/save.ts';
import {
  save as fileSave,
  serverRender as fileServerRender,
} from './file/save.ts';
import {
  save as embedSave,
  serverRender as embedServerRender,
} from './embed/save.ts';

/**
 * Signature of a server-side render hint. Mirrors the
 * `CoreBlock.serverRender` shape in `internal/types.ts`.
 */
export type BlockServerRenderer<A extends BlockAttributes = BlockAttributes> =
  (attrs: A, innerHtml: string) => string;

/**
 * Ordered tuple list of `[blockName, serverRender]` pairs. Consumers
 * iterate this to seed their handler tables. The order matches the
 * inserter ordering in `index.ts::CORE_BLOCKS` so a renderer with a
 * deterministic registration log stays parallel between editor +
 * server.
 */
export const CORE_SERVER_RENDERERS: ReadonlyArray<
  readonly [string, BlockServerRenderer]
> = [
  ['core/paragraph', paragraphServerRender as BlockServerRenderer],
  ['core/heading', headingServerRender as BlockServerRenderer],
  ['core/list', listServerRender as BlockServerRenderer],
  ['core/image', imageServerRender as BlockServerRenderer],
  ['core/quote', quoteServerRender as BlockServerRenderer],
  ['core/code', codeServerRender as BlockServerRenderer],
  ['core/separator', separatorServerRender as BlockServerRenderer],
  ['core/spacer', spacerServerRender as BlockServerRenderer],
  ['core/columns', columnsServerRender as BlockServerRenderer],
  ['core/group', groupServerRender as BlockServerRenderer],
  ['core/table', tableServerRender as BlockServerRenderer],
  ['core/gallery', galleryServerRender as BlockServerRenderer],
  ['core/video', videoServerRender as BlockServerRenderer],
  ['core/button', buttonServerRender as BlockServerRenderer],
  ['core/file', fileServerRender as BlockServerRenderer],
  ['core/embed', embedServerRender as BlockServerRenderer],
];

/**
 * Per-block named exports for consumers that only want a single
 * block's pair (e.g. plugin code wrapping just `core/paragraph`).
 */
export {
  paragraphSave,
  paragraphServerRender,
  headingSave,
  headingServerRender,
  listSave,
  listServerRender,
  imageSave,
  imageServerRender,
  quoteSave,
  quoteServerRender,
  codeSave,
  codeServerRender,
  separatorSave,
  separatorServerRender,
  spacerSave,
  spacerServerRender,
  columnsSave,
  columnsServerRender,
  groupSave,
  groupServerRender,
  tableSave,
  tableServerRender,
  gallerySave,
  galleryServerRender,
  videoSave,
  videoServerRender,
  buttonSave,
  buttonServerRender,
  fileSave,
  fileServerRender,
  embedSave,
  embedServerRender,
};
