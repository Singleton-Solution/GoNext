/**
 * `core/spacer` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { spacerDefinition } from './definition.ts';
import { save, serverRender, type SpacerAttributes } from './save.ts';

export type { SpacerAttributes } from './save.ts';
export { SpacerEdit } from './edit.tsx';

export const spacer: CoreBlock<SpacerAttributes> = {
  definition: spacerDefinition,
  save,
  serverRender,
};
