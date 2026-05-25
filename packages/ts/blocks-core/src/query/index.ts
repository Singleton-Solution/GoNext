/**
 * `core/query` public surface.
 */
import type { CoreBlock } from '../internal/types.ts';
import { queryDefinition } from './definition.ts';
import {
  save,
  serverRender,
  type QueryAttributes,
} from './save.ts';

export type {
  QueryAttributes,
  QueryOrder,
  QueryOrderBy,
} from './save.ts';
export { QueryEdit, summariseQuery } from './edit.tsx';
export { QUERY_INNER_SENTINEL, QUERY_DEFAULTS } from './save.ts';

export const query: CoreBlock<QueryAttributes> = {
  definition: queryDefinition,
  save,
  serverRender,
};
