/**
 * Type definitions for the shared <ResourceList> primitive.
 *
 * See `docs/05-admin-api.md` §2.3 for the design contract: every CRUD list
 * screen in the admin (posts, pages, users, comments, media, …) renders
 * through this single component, so the prop shapes here are deliberately
 * resource-agnostic.
 */
import type { ReactNode } from 'react';

/**
 * A column descriptor for a single resource field.
 *
 * `key` may be a typed field of `T` for compile-time safety, or a free-form
 * string for plugin-registered or derived columns (e.g. `'seo.score'`).
 * When `render` is omitted the cell falls back to stringifying `row[key]`.
 */
export type Column<T> = {
  key: keyof T | string;
  label: string;
  sortable?: boolean;
  render?: (row: T) => ReactNode;
  /** CSS width value, e.g. `'120px'` or `'20%'`. Applied via inline style. */
  width?: string;
};

/**
 * A filter chip — a single-select pill that maps to a URL query parameter.
 *
 * `current` is the currently-selected `value`, or `null` for "any". The
 * parent owns the state; clicking an option in the chip's dropdown fires
 * `onFilterChange(key, value)`.
 */
export type FilterChip = {
  key: string;
  label: string;
  options: { value: string; label: string }[];
  current: string | null;
};

/**
 * A bulk action contract.
 *
 * `confirm`, when present, gates `onApply` behind a confirmation dialog —
 * the string is rendered as the dialog's question. `danger: true` styles the
 * action button as destructive (red). `onApply` receives the array of
 * selected row IDs and may return a Promise the toolbar awaits.
 */
export type BulkAction = {
  id: string;
  label: string;
  confirm?: string;
  danger?: boolean;
  onApply: (selectedIds: string[]) => Promise<void>;
};

/**
 * Sort direction emitted by `onSortChange`. `null` means "no sort applied"
 * — the parent should fall back to whatever default ordering its API uses.
 */
export type SortDirection = 'asc' | 'desc' | null;

/**
 * Cursor-based pagination control.
 *
 * Cursor-style (not page-numbered) because every list endpoint in the admin
 * API exposes cursor pagination — see `docs/05-admin-api.md` §3.1.
 * `cursor` is the *current* cursor; the buttons fire `onNext` / `onPrev`.
 */
export type Pagination = {
  cursor: string | null;
  onNext: () => void;
  onPrev: () => void;
  hasNext?: boolean;
  hasPrev?: boolean;
};

/**
 * Props accepted by `<ResourceList<T>>`.
 *
 * `T` must extend `{ id: string }` because the selection model and bulk
 * actions both key off `row.id`. This is also the shape every admin API
 * resource exposes.
 */
export type ResourceListProps<T extends { id: string }> = {
  columns: Column<T>[];
  data: T[];
  total: number;
  pagination: Pagination;
  filters: FilterChip[];
  bulkActions: BulkAction[];
  onSearch: (q: string) => void;
  onFilterChange?: (key: string, value: string | null) => void;
  onSortChange?: (key: string, direction: SortDirection) => void;
  onRowOpen?: (row: T) => void;
  loading?: boolean;
  emptyState?: ReactNode;
  error?: Error | null;
  onRetry?: () => void;
  /** Optional initial sort, useful for URL-synced views. */
  initialSort?: { key: string; direction: Exclude<SortDirection, null> };
  /** Search debounce in ms. Defaults to 300 per the design doc. */
  searchDebounceMs?: number;
};
