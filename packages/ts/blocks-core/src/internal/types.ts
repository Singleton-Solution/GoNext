/**
 * Shared cross-block types used by every core block module.
 *
 * Each block exports four things, and the shapes below pin the contracts:
 *
 *  - `definition`     — the `BlockTypeDefinition<A>` consumed by the registry
 *  - `Edit`           — the React component the editor mounts
 *  - `save(attrs)`    — pure serialiser returning canonical HTML
 *  - `serverRender(attrs, innerHtml)` — the server-side render hint
 *
 * The `serverRender` channel is what the Go block render walker (see
 * `packages/go/blocks/render`, landed alongside PR #339) calls into.
 * Because the Go side cannot import TS at runtime, we keep the contract
 * documentary here: every core block's `serverRender(attrs, innerHtml)`
 * MUST produce the SAME bytes as the corresponding Go template for the same
 * attributes + already-rendered inner children. That parity is what lets the
 * editor's preview and the published page match.
 *
 * Both `save` and `serverRender` are pure functions of their input — they
 * never touch the DOM, never call hooks, and can run on the server.
 */

import type {
  BlockAttributes,
  BlockSaveProps,
  BlockTypeDefinition,
} from '@gonext/blocks-sdk';

/**
 * A core block module bundles its registration with both serialisation
 * functions so consumers can `import { paragraph } from '@gonext/blocks-core'`
 * and pick the piece they need.
 */
export interface CoreBlock<A extends BlockAttributes> {
  /** The registry definition — lazy `edit`/`save` factories included. */
  definition: BlockTypeDefinition<A>;
  /**
   * Pure serialiser. Same input → same output, no IO. Mirrors the
   * `Save` step in the editor's save pipeline.
   */
  save: (props: BlockSaveProps<A>) => string;
  /**
   * Server-render hint. Takes the block's attributes plus the already-
   * rendered inner-block HTML (empty string for leaf blocks) and returns
   * the wrapper HTML the walker should emit.
   */
  serverRender: (attrs: A, innerHtml: string) => string;
}
