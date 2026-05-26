/**
 * BulkActions — unit tests.
 *
 * Covers:
 *   - Returns nothing when nothing is selected.
 *   - Renders the count + clear button when there is a selection.
 *   - The Delete entry confirms and fires bulkMedia with the right op.
 *   - The AI-alt entry confirms and fires bulkMedia with op=ai-alt.
 *   - A successful op calls onComplete with the parsed result.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const mocks = vi.hoisted(() => ({
  bulkMedia: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { BulkActions } from './BulkActions';

beforeEach(() => {
  mocks.bulkMedia.mockReset();
  vi.spyOn(window, 'confirm').mockReturnValue(true);
});

describe('BulkActions', () => {
  it('renders nothing when the selection is empty', () => {
    const { container } = render(
      <BulkActions selectedIds={[]} onClear={vi.fn()} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders the count and clear button when ids are selected', () => {
    render(<BulkActions selectedIds={['a', 'b']} onClear={vi.fn()} />);
    expect(screen.getByTestId('bulk-actions-count')).toHaveTextContent('2 selected');
    expect(screen.getByTestId('bulk-actions-clear')).toBeInTheDocument();
  });

  it('fires bulkMedia with op=delete when Delete is chosen', async () => {
    mocks.bulkMedia.mockResolvedValueOnce({ op: 'delete', succeeded: 2 });
    const onComplete = vi.fn();
    render(
      <BulkActions
        selectedIds={['a', 'b']}
        onClear={vi.fn()}
        onComplete={onComplete}
      />,
    );
    fireEvent.click(screen.getByTestId('bulk-actions-dropdown'));
    fireEvent.click(screen.getByTestId('bulk-action-delete'));
    await waitFor(() => {
      expect(mocks.bulkMedia).toHaveBeenCalledWith({
        op: 'delete',
        ids: ['a', 'b'],
        params: undefined,
      });
    });
    await waitFor(() => expect(onComplete).toHaveBeenCalledWith({ op: 'delete', succeeded: 2 }));
  });

  it('fires bulkMedia with op=ai-alt when the AI alt-text action is chosen', async () => {
    mocks.bulkMedia.mockResolvedValueOnce({ op: 'ai-alt', succeeded: 1 });
    render(<BulkActions selectedIds={['a']} onClear={vi.fn()} />);
    fireEvent.click(screen.getByTestId('bulk-actions-dropdown'));
    fireEvent.click(screen.getByTestId('bulk-action-ai-alt'));
    await waitFor(() => {
      expect(mocks.bulkMedia).toHaveBeenCalledWith({
        op: 'ai-alt',
        ids: ['a'],
        params: undefined,
      });
    });
    expect(await screen.findByTestId('bulk-actions-result')).toHaveTextContent(
      '1 succeeded',
    );
  });

  it('prompts for tag input on the tag action and passes the trimmed list', async () => {
    mocks.bulkMedia.mockResolvedValueOnce({ op: 'tag', succeeded: 1 });
    vi.spyOn(window, 'prompt').mockReturnValueOnce(' hero,  banner  , ');
    render(<BulkActions selectedIds={['a']} onClear={vi.fn()} />);
    fireEvent.click(screen.getByTestId('bulk-actions-dropdown'));
    fireEvent.click(screen.getByTestId('bulk-action-tag'));
    await waitFor(() => {
      expect(mocks.bulkMedia).toHaveBeenCalledWith({
        op: 'tag',
        ids: ['a'],
        params: { add: ['hero', 'banner'] },
      });
    });
  });
});
