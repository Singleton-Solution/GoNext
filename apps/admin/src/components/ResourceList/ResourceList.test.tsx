/**
 * <ResourceList> tests.
 *
 * Covers the behaviour spelled out in issue #25's acceptance criteria:
 *  - Empty data shows the empty state.
 *  - N rows render N row checkboxes plus the select-all.
 *  - Select-all toggles all rows on, then all off.
 *  - A bulk action with `confirm` opens a confirmation dialog before
 *    invoking `onApply`.
 *  - Clicking a filter-chip option fires `onFilterChange(key, value)`.
 *  - Typing in the search input fires a *debounced* `onSearch`.
 *  - Clicking a sortable header cycles asc → desc → null, emitting
 *    `onSortChange(key, direction)` on each click.
 *  - Error state shows the inline "Couldn't load. Retry" message.
 *
 * Tests use fake timers around the debounce assertion only, because the
 * other interactions are synchronous and benefit from real timers.
 */
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, within } from '@testing-library/react';

import { ResourceList } from './ResourceList';
import type { BulkAction, Column, FilterChip, Pagination } from './types';

type Row = {
  id: string;
  title: string;
  author: string;
};

const COLUMNS: Column<Row>[] = [
  { key: 'title', label: 'Title', sortable: true },
  { key: 'author', label: 'Author' },
];

const ROWS: Row[] = [
  { id: '1', title: 'Hello World', author: 'admin' },
  { id: '2', title: 'Draft notes', author: 'editor' },
  { id: '3', title: 'Third post', author: 'admin' },
];

function makePagination(overrides: Partial<Pagination> = {}): Pagination {
  return {
    cursor: null,
    onNext: vi.fn(),
    onPrev: vi.fn(),
    hasNext: true,
    hasPrev: false,
    ...overrides,
  };
}

const NOOP_SEARCH = vi.fn();

describe('ResourceList', () => {
  it('renders the empty state when data is empty', () => {
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={[]}
        total={0}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    expect(screen.getByTestId('resource-list-empty')).toHaveTextContent(
      /no items/i,
    );
  });

  it('renders the supplied custom emptyState when provided', () => {
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={[]}
        total={0}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        emptyState={<span>Nothing here yet — create one.</span>}
      />,
    );

    expect(screen.getByTestId('resource-list-empty')).toHaveTextContent(
      /nothing here yet/i,
    );
  });

  it('renders one row per data item with a checkbox each', () => {
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    for (const row of ROWS) {
      expect(screen.getByTestId(`resource-row-${row.id}`)).toBeInTheDocument();
    }

    // 3 row checkboxes + 1 select-all checkbox = 4 total.
    const checkboxes = screen.getAllByRole('checkbox');
    expect(checkboxes).toHaveLength(ROWS.length + 1);
  });

  it('toggles all rows on then off via the select-all checkbox', () => {
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    const selectAll = screen.getByTestId(
      'resource-list-select-all',
    ) as HTMLInputElement;

    // First click → all rows selected.
    fireEvent.click(selectAll);
    expect(selectAll.checked).toBe(true);
    expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent(
      `${ROWS.length} selected`,
    );
    for (const row of ROWS) {
      const rowEl = screen.getByTestId(`resource-row-${row.id}`);
      expect(rowEl).toHaveAttribute('aria-selected', 'true');
    }

    // Second click → all rows deselected, and the bulk bar disappears.
    fireEvent.click(selectAll);
    expect(selectAll.checked).toBe(false);
    expect(screen.queryByTestId('bulk-selected-count')).not.toBeInTheDocument();
  });

  it('opens a confirmation dialog when a bulk action with `confirm` is clicked', async () => {
    const apply = vi.fn().mockResolvedValue(undefined);
    const action: BulkAction = {
      id: 'trash',
      label: 'Trash',
      confirm: 'Move 2 posts to trash?',
      danger: true,
      onApply: apply,
    };

    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[action]}
        onSearch={NOOP_SEARCH}
      />,
    );

    // Select two rows so the bulk bar appears.
    fireEvent.click(screen.getByLabelText('Select row 1'));
    fireEvent.click(screen.getByLabelText('Select row 2'));

    // Click the bulk action.
    fireEvent.click(screen.getByTestId('bulk-action-trash'));

    // Dialog is visible with the confirm prompt; onApply has NOT fired yet.
    const dialog = screen.getByTestId('resource-list-confirm');
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByText('Move 2 posts to trash?')).toBeInTheDocument();
    expect(apply).not.toHaveBeenCalled();

    // Cancel closes without running.
    fireEvent.click(screen.getByTestId('resource-list-confirm-cancel'));
    expect(screen.queryByTestId('resource-list-confirm')).not.toBeInTheDocument();
    expect(apply).not.toHaveBeenCalled();

    // Re-open and confirm → onApply runs with the selected IDs. Wrap in
    // act() because confirming awaits the async onApply, and we want the
    // post-resolution state flush (clear selection) to land before the
    // test ends.
    fireEvent.click(screen.getByTestId('bulk-action-trash'));
    await act(async () => {
      fireEvent.click(screen.getByTestId('resource-list-confirm-apply'));
    });

    expect(apply).toHaveBeenCalledTimes(1);
    expect(apply).toHaveBeenCalledWith(expect.arrayContaining(['1', '2']));
    expect((apply.mock.calls[0]?.[0] as string[]).length).toBe(2);
  });

  it('skips the dialog and runs immediately for actions without a confirm prop', async () => {
    const apply = vi.fn().mockResolvedValue(undefined);
    const action: BulkAction = {
      id: 'star',
      label: 'Star',
      onApply: apply,
    };

    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[action]}
        onSearch={NOOP_SEARCH}
      />,
    );

    fireEvent.click(screen.getByLabelText('Select row 1'));
    await act(async () => {
      fireEvent.click(screen.getByTestId('bulk-action-star'));
    });

    expect(screen.queryByTestId('resource-list-confirm')).not.toBeInTheDocument();
    expect(apply).toHaveBeenCalledWith(['1']);
  });

  it('fires onFilterChange when a chip option is selected', () => {
    const onFilterChange = vi.fn();
    const chips: FilterChip[] = [
      {
        key: 'status',
        label: 'Status',
        current: null,
        options: [
          { value: 'draft', label: 'Draft' },
          { value: 'published', label: 'Published' },
        ],
      },
    ];

    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={chips}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        onFilterChange={onFilterChange}
      />,
    );

    // Open the chip menu, then pick "Draft".
    fireEvent.click(screen.getByTestId('filter-chip-status'));
    fireEvent.click(screen.getByTestId('filter-option-status-draft'));

    expect(onFilterChange).toHaveBeenCalledWith('status', 'draft');
  });

  it('debounces the search input by ~300ms before calling onSearch', () => {
    vi.useFakeTimers();
    try {
      const onSearch = vi.fn();
      render(
        <ResourceList<Row>
          columns={COLUMNS}
          data={ROWS}
          total={ROWS.length}
          pagination={makePagination()}
          filters={[]}
          bulkActions={[]}
          onSearch={onSearch}
        />,
      );

      const input = screen.getByTestId('resource-list-search');
      fireEvent.change(input, { target: { value: 'h' } });
      fireEvent.change(input, { target: { value: 'he' } });
      fireEvent.change(input, { target: { value: 'hello' } });

      // Mid-typing: nothing fired yet.
      expect(onSearch).not.toHaveBeenCalled();

      // Halfway through the debounce window: still nothing.
      vi.advanceTimersByTime(150);
      expect(onSearch).not.toHaveBeenCalled();

      // Past the window: exactly one call, with the latest value.
      vi.advanceTimersByTime(200);
      expect(onSearch).toHaveBeenCalledTimes(1);
      expect(onSearch).toHaveBeenCalledWith('hello');
    } finally {
      vi.useRealTimers();
    }
  });

  it('cycles sort direction asc → desc → null on repeated header clicks', () => {
    const onSortChange = vi.fn();
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        onSortChange={onSortChange}
      />,
    );

    const header = screen.getByTestId('column-header-title');

    fireEvent.click(header);
    expect(onSortChange).toHaveBeenNthCalledWith(1, 'title', 'asc');
    expect(header).toHaveAttribute('aria-sort', 'ascending');

    fireEvent.click(header);
    expect(onSortChange).toHaveBeenNthCalledWith(2, 'title', 'desc');
    expect(header).toHaveAttribute('aria-sort', 'descending');

    fireEvent.click(header);
    expect(onSortChange).toHaveBeenNthCalledWith(3, 'title', null);
    expect(header).toHaveAttribute('aria-sort', 'none');
  });

  it('does not call onSortChange for non-sortable columns', () => {
    const onSortChange = vi.fn();
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        onSortChange={onSortChange}
      />,
    );

    fireEvent.click(screen.getByTestId('column-header-author'));
    expect(onSortChange).not.toHaveBeenCalled();
  });

  it('shows skeleton rows while loading with no data', () => {
    const { container } = render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={[]}
        total={0}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        loading
      />,
    );

    // Six skeleton rows live in the tbody and are aria-hidden.
    const skeletonRows = container.querySelectorAll('tbody tr[aria-hidden="true"]');
    expect(skeletonRows.length).toBeGreaterThan(0);
    expect(screen.queryByTestId('resource-list-empty')).not.toBeInTheDocument();
  });

  it('renders an inline error message with a Retry button when error is set', () => {
    const onRetry = vi.fn();
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={[]}
        total={0}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        error={new Error('Network down')}
        onRetry={onRetry}
      />,
    );

    expect(screen.getByRole('alert')).toHaveTextContent(/couldn't load/i);
    fireEvent.click(screen.getByTestId('resource-list-retry'));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('supports keyboard navigation: ArrowDown moves focus, Space toggles, Enter opens', () => {
    const onRowOpen = vi.fn();
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
        onRowOpen={onRowOpen}
      />,
    );

    const firstRow = screen.getByTestId('resource-row-1');
    firstRow.focus();
    expect(document.activeElement).toBe(firstRow);

    fireEvent.keyDown(firstRow, { key: 'ArrowDown' });
    const secondRow = screen.getByTestId('resource-row-2');
    expect(document.activeElement).toBe(secondRow);

    fireEvent.keyDown(secondRow, { key: ' ' });
    expect(secondRow).toHaveAttribute('aria-selected', 'true');

    fireEvent.keyDown(secondRow, { key: 'Enter' });
    expect(onRowOpen).toHaveBeenCalledWith(ROWS[1]);
  });

  it('uses a custom render function for column cells when provided', () => {
    const columns: Column<Row>[] = [
      {
        key: 'title',
        label: 'Title',
        render: (row) => <strong data-testid={`cell-${row.id}`}>{row.title.toUpperCase()}</strong>,
      },
    ];

    render(
      <ResourceList<Row>
        columns={columns}
        data={ROWS.slice(0, 1)}
        total={1}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    expect(screen.getByTestId('cell-1')).toHaveTextContent('HELLO WORLD');
  });

  it('fires pagination handlers when Next/Prev are clicked', () => {
    const onNext = vi.fn();
    const onPrev = vi.fn();
    render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={{
          cursor: 'abc',
          onNext,
          onPrev,
          hasNext: true,
          hasPrev: true,
        }}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    fireEvent.click(screen.getByTestId('resource-list-next'));
    fireEvent.click(screen.getByTestId('resource-list-prev'));
    expect(onNext).toHaveBeenCalledTimes(1);
    expect(onPrev).toHaveBeenCalledTimes(1);
  });

  it('clears selections that are no longer present in the data', () => {
    const { rerender } = render(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS}
        total={ROWS.length}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    fireEvent.click(screen.getByLabelText('Select row 2'));
    expect(screen.getByTestId('bulk-selected-count')).toHaveTextContent(
      '1 selected',
    );

    // Re-render with row 2 no longer in the dataset.
    rerender(
      <ResourceList<Row>
        columns={COLUMNS}
        data={ROWS.filter((row) => row.id !== '2')}
        total={ROWS.length - 1}
        pagination={makePagination()}
        filters={[]}
        bulkActions={[]}
        onSearch={NOOP_SEARCH}
      />,
    );

    expect(screen.queryByTestId('bulk-selected-count')).not.toBeInTheDocument();
  });
});
