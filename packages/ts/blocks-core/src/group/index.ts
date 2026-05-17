/**
 * `core/group` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { groupDefinition } from './definition.ts';
import { save, serverRender, type GroupAttributes } from './save.ts';

export type { GroupAttributes } from './save.ts';
export { GroupEdit } from './edit.tsx';
export { GROUP_INNER_SENTINEL } from './save.ts';

export const group: CoreBlock<GroupAttributes> = {
  definition: groupDefinition,
  save,
  serverRender,
};
