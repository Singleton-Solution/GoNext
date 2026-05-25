/**
 * `core/media-text` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { mediaTextDefinition } from './definition.ts';
import { save, serverRender, type MediaTextAttributes } from './save.ts';

export type { MediaTextAttributes } from './save.ts';
export { MediaTextEdit } from './edit.tsx';
export {
  MEDIA_TEXT_INNER_SENTINEL,
  normalizeMediaWidth,
} from './save.ts';

export const mediaText: CoreBlock<MediaTextAttributes> = {
  definition: mediaTextDefinition,
  save,
  serverRender,
};
