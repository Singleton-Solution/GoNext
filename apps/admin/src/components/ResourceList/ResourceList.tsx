'use client';

/**
 * <ResourceList<T>> — the shared shell for every admin list screen.
 *
 * Per `docs/05-admin-api.md` §2.3, every CRUD list in the admin (posts,
 * pages, users, comments, media, custom post types) shares this component.
 * It owns the *shape* of a list — search, filters, sortable columns,
 * selection, bulk actions, pagination, empty/loading/error states — and
 * delegates data fetching to its parent.
 *
 * Design notes:
 *
 *  - State that the parent will want to URL-sync (search, filters, sort,
 *    pagination) is *raised*. We only own internal UI state: which rows are
 *    selected, which sort-direction the user just toggled to, which chip
 *    menu is open, and the pending bulk-action confirmation.
 *
 *  - Search is debounced (300ms by default, configurable) inside the
 *    component so callers don't each have to wire up their own debouncer.
 *    Filter and sort changes are eager because they're triggered by a
 *    discrete click rather than typing.
 *
 *  - Sort cycles through asc → desc → null on repeated clicks of the same
 *    column. Clicking a different column resets to asc. Only one sort key
 *    at a time — the multi-sort shift-click behaviour described in the doc
 *    is a follow-up.
 *
 *  - Keyboard model on the row tbody: rows are `tabIndex=0`. ArrowDown /
 *    ArrowUp move focus between rows; Space toggles selection on the
 *    focused row; Enter calls `onRowOpen(row)` if provided.
 *
 *  - The bulk-action confirmation dialog is rendered inline (no portal) for
 *    test simplicity. Once the design system extracts a Dialog primitive
 *    (issue #34) we can swap this out without changing the prop contract.
 */
import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import styles from './ResourceList.module.css';
import type {
  BulkAction,
  Column,
  FilterChip,
  ResourceListProps,
  SortDirection,
} from './types';

const DEFAULT_DEBOUNCE_MS = 300;

/** Cycle order for header clicks: asc → desc → null → asc. */
function nextSortDirection(current: SortDirection): SortDirection {
  if (current === null) return 'asc';
  if (current === 'asc') return 'desc';
  return null;
}

function renderCell<T>(column: Column<T>, row: T): ReactNode {
  if (column.render) return column.render(row);
  // `key` may not be a typed field — index defensively.
  const value = (row as Record<string, unknown>)[column.key as string];
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

export function ResourceList<T extends { id: string }>({
  columns,
  data,
  total,
  pagination,
  filters,
  bulkActions,
  onSearch,
  onFilterChange,
  onSortChange,
  onRowOpen,
  loading = false,
  emptyState,
  error = null,
  onRetry,
  initialSort,
  searchDebounceMs = DEFAULT_DEBOUNCE_MS,
}: ResourceListProps<T>): ReactElement {
  // ── selection ──────────────────────────────────────────────────────────
  const [selectedIds, setSelectedIds] = useState<ReadonlySet<string>>(
    () => new Set<string>(),
  );

  // When the underlying data changes (e.g. user navigates a page or applies
  // a filter), drop selections that are no longer visible. This avoids
  // bulk-acting on rows the user can't see.
  useEffect(() => {
    setSelectedIds((prev) => {
      if (prev.size === 0) return prev;
      const visible = new Set(data.map((row) => row.id));
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (visible.has(id)) {
          next.add(id);
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [data]);

  const allSelected = data.length > 0 && selectedIds.size === data.length;
  const someSelected = selectedIds.size > 0 && !allSelected;

  const toggleRow = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const toggleAll = useCallback(() => {
    setSelectedIds((prev) => {
      if (prev.size === data.length && data.length > 0) {
        return new Set<string>();
      }
      return new Set(data.map((row) => row.id));
    });
  }, [data]);

  // ── search (debounced) ─────────────────────────────────────────────────
  const [searchValue, setSearchValue] = useState('');
  const debounceTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    // Cleanup on unmount — don't fire a stale search into a dead tree.
    return () => {
      if (debounceTimer.current !== null) {
        clearTimeout(debounceTimer.current);
      }
    };
  }, []);

  const handleSearchChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const value = event.target.value;
      setSearchValue(value);
      if (debounceTimer.current !== null) {
        clearTimeout(debounceTimer.current);
      }
      debounceTimer.current = setTimeout(() => {
        onSearch(value);
      }, searchDebounceMs);
    },
    [onSearch, searchDebounceMs],
  );

  // ── sort ───────────────────────────────────────────────────────────────
  const [sortKey, setSortKey] = useState<string | null>(
    initialSort?.key ?? null,
  );
  const [sortDirection, setSortDirection] = useState<SortDirection>(
    initialSort?.direction ?? null,
  );

  const handleHeaderClick = useCallback(
    (column: Column<T>) => {
      if (!column.sortable) return;
      const key = String(column.key);
      const isSame = key === sortKey;
      const nextDir = isSame ? nextSortDirection(sortDirection) : 'asc';
      if (nextDir === null) {
        setSortKey(null);
        setSortDirection(null);
        onSortChange?.(key, null);
      } else {
        setSortKey(key);
        setSortDirection(nextDir);
        onSortChange?.(key, nextDir);
      }
    },
    [onSortChange, sortDirection, sortKey],
  );

  // ── filter chips ───────────────────────────────────────────────────────
  const [openChipKey, setOpenChipKey] = useState<string | null>(null);

  const handleChipSelect = useCallback(
    (chip: FilterChip, value: string | null) => {
      setOpenChipKey(null);
      onFilterChange?.(chip.key, value);
    },
    [onFilterChange],
  );

  // ── bulk-action confirmation ───────────────────────────────────────────
  const [pendingAction, setPendingAction] = useState<BulkAction | null>(null);

  const runAction = useCallback(
    async (action: BulkAction) => {
      const ids = Array.from(selectedIds);
      await action.onApply(ids);
      // After a successful bulk action, clear selection — the parent will
      // typically refetch and the IDs may no longer match.
      setSelectedIds(new Set<string>());
    },
    [selectedIds],
  );

  const handleBulkClick = useCallback(
    (action: BulkAction) => {
      if (action.confirm) {
        setPendingAction(action);
      } else {
        void runAction(action);
      }
    },
    [runAction],
  );

  const confirmPending = useCallback(async () => {
    if (pendingAction === null) return;
    const action = pendingAction;
    setPendingAction(null);
    await runAction(action);
  }, [pendingAction, runAction]);

  // ── keyboard navigation on rows ────────────────────────────────────────
  const rowRefs = useRef<Map<string, HTMLTableRowElement>>(new Map());

  const handleRowKeyDown = useCallback(
    (event: KeyboardEvent<HTMLTableRowElement>, row: T, index: number) => {
      switch (event.key) {
        case 'ArrowDown': {
          event.preventDefault();
          const next = data[index + 1];
          if (next) {
            rowRefs.current.get(next.id)?.focus();
          }
          break;
        }
        case 'ArrowUp': {
          event.preventDefault();
          const prev = data[index - 1];
          if (prev) {
            rowRefs.current.get(prev.id)?.focus();
          }
          break;
        }
        case ' ': {
          event.preventDefault();
          toggleRow(row.id);
          break;
        }
        case 'Enter': {
          if (onRowOpen) {
            event.preventDefault();
            onRowOpen(row);
          }
          break;
        }
        default:
          break;
      }
    },
    [data, onRowOpen, toggleRow],
  );

  const selectAllId = useId();
  const captionId = useId();

  // ── render branches ────────────────────────────────────────────────────
  const showSkeleton = loading && data.length === 0;
  const showEmpty = !loading && !error && data.length === 0;
  const showError = error !== null && error !== undefined;

  const renderBody = useMemo<ReactNode>(() => {
    if (showSkeleton) {
      // Render six skeleton rows; the +1 accounts for the checkbox column.
      const skeletonRows = Array.from({ length: 6 }).map((_, rowIndex) => (
        <tr key={`skeleton-${rowIndex}`} aria-hidden="true">
          <td className={`${styles.td} ${styles.checkboxCell}`}>
            <div className={styles.skeletonCell} />
          </td>
          {columns.map((column) => (
            <td
              key={String(column.key)}
              className={styles.td}
              style={column.width ? { width: column.width } : undefined}
            >
              <div className={styles.skeletonCell} />
            </td>
          ))}
        </tr>
      ));
      return skeletonRows;
    }

    if (showEmpty) {
      return (
        <tr>
          <td
            colSpan={columns.length + 1}
            className={`${styles.td} ${styles.empty}`}
            data-testid="resource-list-empty"
          >
            {emptyState ?? <span>No items.</span>}
          </td>
        </tr>
      );
    }

    return data.map((row, index) => {
      const isSelected = selectedIds.has(row.id);
      return (
        <tr
          key={row.id}
          ref={(node) => {
            if (node) {
              rowRefs.current.set(row.id, node);
            } else {
              rowRefs.current.delete(row.id);
            }
          }}
          tabIndex={0}
          role="row"
          aria-selected={isSelected}
          data-testid={`resource-row-${row.id}`}
          className={isSelected ? `${styles.row} ${styles.rowSelected}` : styles.row}
          onKeyDown={(event) => handleRowKeyDown(event, row, index)}
        >
          <td className={`${styles.td} ${styles.checkboxCell}`}>
            <input
              type="checkbox"
              aria-label={`Select row ${row.id}`}
              checked={isSelected}
              onChange={() => toggleRow(row.id)}
              onClick={(event) => event.stopPropagation()}
            />
          </td>
          {columns.map((column) => (
            <td
              key={String(column.key)}
              className={styles.td}
              style={column.width ? { width: column.width } : undefined}
              onClick={() => onRowOpen?.(row)}
            >
              {renderCell(column, row)}
            </td>
          ))}
        </tr>
      );
    });
  }, [
    columns,
    data,
    emptyState,
    handleRowKeyDown,
    onRowOpen,
    selectedIds,
    showEmpty,
    showSkeleton,
    toggleRow,
  ]);

  return (
    <div className={styles.root} data-testid="resource-list">
      <div className={styles.toolbar} role="toolbar" aria-label="List controls">
        <input
          type="search"
          className={styles.search}
          placeholder="Search"
          aria-label="Search"
          value={searchValue}
          onChange={handleSearchChange}
          data-testid="resource-list-search"
        />
        <div className={styles.filters} role="group" aria-label="Filters">
          {filters.map((chip) => {
            const isOpen = openChipKey === chip.key;
            const activeOption = chip.options.find(
              (opt) => opt.value === chip.current,
            );
            const labelText = activeOption
              ? `${chip.label}: ${activeOption.label}`
              : chip.label;
            return (
              <div key={chip.key} className={styles.chipMenu}>
                <button
                  type="button"
                  className={
                    chip.current !== null
                      ? `${styles.chip} ${styles.chipActive}`
                      : styles.chip
                  }
                  aria-pressed={chip.current !== null}
                  aria-haspopup="menu"
                  aria-expanded={isOpen}
                  onClick={() => setOpenChipKey(isOpen ? null : chip.key)}
                  data-testid={`filter-chip-${chip.key}`}
                >
                  {labelText}
                </button>
                {isOpen ? (
                  <div className={styles.chipMenuPanel} role="menu">
                    <button
                      type="button"
                      className={styles.chipMenuItem}
                      role="menuitem"
                      onClick={() => handleChipSelect(chip, null)}
                    >
                      Any
                    </button>
                    {chip.options.map((option) => (
                      <button
                        key={option.value}
                        type="button"
                        className={styles.chipMenuItem}
                        role="menuitem"
                        onClick={() => handleChipSelect(chip, option.value)}
                        data-testid={`filter-option-${chip.key}-${option.value}`}
                      >
                        {option.label}
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      </div>

      {selectedIds.size > 0 ? (
        <div className={styles.bulkBar} role="region" aria-label="Bulk actions">
          <span className={styles.bulkBarCount} data-testid="bulk-selected-count">
            {selectedIds.size} selected
          </span>
          {bulkActions.map((action) => (
            <button
              key={action.id}
              type="button"
              className={
                action.danger
                  ? `${styles.bulkButton} ${styles.bulkButtonDanger}`
                  : styles.bulkButton
              }
              onClick={() => handleBulkClick(action)}
              data-testid={`bulk-action-${action.id}`}
            >
              {action.label}
            </button>
          ))}
        </div>
      ) : null}

      {showError ? (
        <div className={styles.errorBox} role="alert">
          <span>Couldn&apos;t load.</span>
          {onRetry ? (
            <button
              type="button"
              className={styles.errorRetry}
              onClick={onRetry}
              data-testid="resource-list-retry"
            >
              Retry
            </button>
          ) : null}
        </div>
      ) : (
        <div className={styles.tableWrap}>
          <table className={styles.table} aria-labelledby={captionId}>
            <caption id={captionId} className={styles.empty} hidden>
              Resource list
            </caption>
            <thead>
              <tr>
                <th className={`${styles.th} ${styles.checkboxCell}`} scope="col">
                  <input
                    id={selectAllId}
                    type="checkbox"
                    aria-label="Select all rows"
                    checked={allSelected}
                    ref={(input) => {
                      // jsdom supports indeterminate; native checkboxes don't
                      // expose a ref prop equivalent so we set it manually.
                      if (input) {
                        input.indeterminate = someSelected;
                      }
                    }}
                    onChange={toggleAll}
                    disabled={data.length === 0}
                    data-testid="resource-list-select-all"
                  />
                </th>
                {columns.map((column) => {
                  const key = String(column.key);
                  const isSorted = sortKey === key;
                  const indicator = isSorted
                    ? sortDirection === 'asc'
                      ? '▲'
                      : sortDirection === 'desc'
                        ? '▼'
                        : ''
                    : '';
                  return (
                    <th
                      key={key}
                      scope="col"
                      className={
                        column.sortable
                          ? `${styles.th} ${styles.thSortable}`
                          : styles.th
                      }
                      style={column.width ? { width: column.width } : undefined}
                      aria-sort={
                        isSorted && sortDirection !== null
                          ? sortDirection === 'asc'
                            ? 'ascending'
                            : 'descending'
                          : column.sortable
                            ? 'none'
                            : undefined
                      }
                      onClick={
                        column.sortable
                          ? () => handleHeaderClick(column)
                          : undefined
                      }
                      onKeyDown={
                        column.sortable
                          ? (event) => {
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault();
                                handleHeaderClick(column);
                              }
                            }
                          : undefined
                      }
                      tabIndex={column.sortable ? 0 : undefined}
                      role={column.sortable ? 'columnheader button' : 'columnheader'}
                      data-testid={`column-header-${key}`}
                    >
                      {column.label}
                      <span className={styles.sortIndicator} aria-hidden="true">
                        {indicator}
                      </span>
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody>{renderBody}</tbody>
          </table>
        </div>
      )}

      <div className={styles.footer}>
        <span data-testid="resource-list-total">
          {total} {total === 1 ? 'item' : 'items'}
        </span>
        <div className={styles.pager}>
          <button
            type="button"
            className={styles.pagerButton}
            onClick={pagination.onPrev}
            disabled={pagination.hasPrev === false}
            data-testid="resource-list-prev"
          >
            Prev
          </button>
          <button
            type="button"
            className={styles.pagerButton}
            onClick={pagination.onNext}
            disabled={pagination.hasNext === false}
            data-testid="resource-list-next"
          >
            Next
          </button>
        </div>
      </div>

      {pendingAction !== null ? (
        <div
          className={styles.confirmBackdrop}
          role="dialog"
          aria-modal="true"
          aria-labelledby="resource-list-confirm-title"
          data-testid="resource-list-confirm"
        >
          <div className={styles.confirmDialog}>
            <h2 id="resource-list-confirm-title">{pendingAction.label}</h2>
            <p>{pendingAction.confirm}</p>
            <div className={styles.confirmActions}>
              <button
                type="button"
                className={styles.bulkButton}
                onClick={() => setPendingAction(null)}
                data-testid="resource-list-confirm-cancel"
              >
                Cancel
              </button>
              <button
                type="button"
                className={
                  pendingAction.danger
                    ? `${styles.bulkButton} ${styles.bulkButtonDanger}`
                    : styles.bulkButton
                }
                onClick={() => void confirmPending()}
                data-testid="resource-list-confirm-apply"
              >
                Confirm
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
