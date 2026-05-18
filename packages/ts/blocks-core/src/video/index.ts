/**
 * `core/video` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { videoDefinition } from './definition.ts';
import { save, serverRender, type VideoAttributes } from './save.ts';

export type { VideoAttributes } from './save.ts';
export { VideoEdit } from './edit.tsx';

export const video: CoreBlock<VideoAttributes> = {
  definition: videoDefinition,
  save,
  serverRender,
};
