/**
 * `core/embed` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { embedDefinition } from './definition.ts';
import { save, serverRender, type EmbedAttributes } from './save.ts';

export type { EmbedAttributes } from './save.ts';
export type { EmbedProvider } from './providers.ts';
export { detectProvider, EMBED_PROVIDERS } from './providers.ts';
export { EmbedEdit } from './edit.tsx';

export const embed: CoreBlock<EmbedAttributes> = {
  definition: embedDefinition,
  save,
  serverRender,
};
