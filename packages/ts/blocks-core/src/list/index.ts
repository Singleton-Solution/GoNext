/**
 * `core/list` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { listDefinition } from './definition.ts';
import { save, serverRender, type ListAttributes } from './save.ts';

export type { ListAttributes } from './save.ts';
export { ListEdit } from './edit.tsx';

export const list: CoreBlock<ListAttributes> = {
  definition: listDefinition,
  save,
  serverRender,
};
