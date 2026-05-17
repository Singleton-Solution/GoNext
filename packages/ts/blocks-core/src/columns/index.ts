/**
 * `core/columns` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { columnsDefinition } from './definition.ts';
import { save, serverRender, type ColumnsAttributes } from './save.ts';

export type { ColumnsAttributes } from './save.ts';
export { ColumnsEdit } from './edit.tsx';
export { COLUMNS_INNER_SENTINEL } from './save.ts';

export const columns: CoreBlock<ColumnsAttributes> = {
  definition: columnsDefinition,
  save,
  serverRender,
};
