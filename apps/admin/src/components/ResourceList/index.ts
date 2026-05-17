/**
 * Public surface of the shared <ResourceList> primitive.
 *
 * Consumers (post list, user list, comment inbox, …) should import from
 * `@/components/ResourceList` — never from the .tsx file directly — so
 * future internal refactors stay non-breaking.
 *
 * See `docs/05-admin-api.md` §2.3 for the design contract.
 */
export { ResourceList } from './ResourceList';
export type {
  BulkAction,
  Column,
  FilterChip,
  Pagination,
  ResourceListProps,
  SortDirection,
} from './types';
