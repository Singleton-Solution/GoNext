/**
 * `core/table` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { tableDefinition } from './definition.ts';
import { save, serverRender, type TableAttributes } from './save.ts';

export type { TableAttributes, TableRow } from './save.ts';
export { TableEdit } from './edit.tsx';

export const table: CoreBlock<TableAttributes> = {
  definition: tableDefinition,
  save,
  serverRender,
};
