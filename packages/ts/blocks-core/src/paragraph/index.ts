/**
 * `core/paragraph` public surface.
 *
 * Exports the bundle in the canonical `CoreBlock<A>` shape used by every
 * core block module. Consumers `import { paragraph } from '@gonext/blocks-core'`
 * and pick what they need (definition for the registry, save for the
 * canvas, serverRender for the SSR walker).
 */
import type { CoreBlock } from '../internal/types.ts';
import { paragraphDefinition } from './definition.ts';
import { save, serverRender, type ParagraphAttributes } from './save.ts';

export type { ParagraphAttributes } from './save.ts';
export { ParagraphEdit } from './edit.tsx';

export const paragraph: CoreBlock<ParagraphAttributes> = {
  definition: paragraphDefinition,
  save,
  serverRender,
};
