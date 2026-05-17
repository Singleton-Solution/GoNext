/**
 * `core/image` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { imageDefinition } from './definition.ts';
import { save, serverRender, type ImageAttributes } from './save.ts';

export type { ImageAttributes } from './save.ts';
export { ImageEdit } from './edit.tsx';

export const image: CoreBlock<ImageAttributes> = {
  definition: imageDefinition,
  save,
  serverRender,
};
