/**
 * `core/heading` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { headingDefinition } from './definition.ts';
import { save, serverRender, type HeadingAttributes } from './save.ts';

export type { HeadingAttributes } from './save.ts';
export { HeadingEdit } from './edit.tsx';

export const heading: CoreBlock<HeadingAttributes> = {
  definition: headingDefinition,
  save,
  serverRender,
};
