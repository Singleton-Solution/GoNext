/**
 * `core/gallery` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { galleryDefinition } from './definition.ts';
import { save, serverRender, type GalleryAttributes } from './save.ts';

export type { GalleryAttributes, GalleryImage } from './save.ts';
export { GalleryEdit } from './edit.tsx';

export const gallery: CoreBlock<GalleryAttributes> = {
  definition: galleryDefinition,
  save,
  serverRender,
};
