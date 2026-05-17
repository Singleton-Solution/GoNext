/**
 * `core/separator` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { separatorDefinition } from './definition.ts';
import { save, serverRender, type SeparatorAttributes } from './save.ts';

export type { SeparatorAttributes } from './save.ts';
export { SeparatorEdit } from './edit.tsx';

export const separator: CoreBlock<SeparatorAttributes> = {
  definition: separatorDefinition,
  save,
  serverRender,
};
