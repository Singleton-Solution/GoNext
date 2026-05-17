/**
 * `core/code` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { codeDefinition } from './definition.ts';
import { save, serverRender, type CodeAttributes } from './save.ts';

export type { CodeAttributes } from './save.ts';
export { CodeEdit } from './edit.tsx';

export const code: CoreBlock<CodeAttributes> = {
  definition: codeDefinition,
  save,
  serverRender,
};
