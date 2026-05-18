/**
 * `core/button` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { buttonDefinition } from './definition.ts';
import { save, serverRender, type ButtonAttributes } from './save.ts';

export type { ButtonAttributes } from './save.ts';
export { ButtonEdit } from './edit.tsx';

export const button: CoreBlock<ButtonAttributes> = {
  definition: buttonDefinition,
  save,
  serverRender,
};
