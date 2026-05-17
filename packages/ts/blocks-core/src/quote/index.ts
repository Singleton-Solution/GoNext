/**
 * `core/quote` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { quoteDefinition } from './definition.ts';
import { save, serverRender, type QuoteAttributes } from './save.ts';

export type { QuoteAttributes } from './save.ts';
export { QuoteEdit } from './edit.tsx';

export const quote: CoreBlock<QuoteAttributes> = {
  definition: quoteDefinition,
  save,
  serverRender,
};
