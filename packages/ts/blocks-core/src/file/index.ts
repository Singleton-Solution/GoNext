/**
 * `core/file` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { fileDefinition } from './definition.ts';
import { save, serverRender, type FileAttributes } from './save.ts';

export type { FileAttributes } from './save.ts';
export { FileEdit } from './edit.tsx';

export const file: CoreBlock<FileAttributes> = {
  definition: fileDefinition,
  save,
  serverRender,
};
