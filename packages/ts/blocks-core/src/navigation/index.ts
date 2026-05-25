/**
 * `core/navigation` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { navigationDefinition } from './definition.ts';
import {
  save,
  serverRender,
  type NavigationAttributes,
} from './save.ts';

export type { NavigationAttributes, NavigationItem } from './save.ts';
export { NavigationEdit } from './edit.tsx';
export { DEFAULT_NAV_ARIA_LABEL } from './save.ts';

export const navigation: CoreBlock<NavigationAttributes> = {
  definition: navigationDefinition,
  save,
  serverRender,
};
