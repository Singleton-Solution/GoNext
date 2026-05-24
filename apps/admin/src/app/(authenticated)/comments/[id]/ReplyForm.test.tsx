/**
 * Tests for the ReplyForm island.
 *
 * Coverage:
 *   - Post button is disabled until content is non-empty.
 *   - Submitting calls the supplied poster with trimmed content.
 *   - Failure renders an inline alert.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';

const mockRefresh = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ refresh: mockRefresh }),
}));

import { ReplyForm } from './ReplyForm';

describe('ReplyForm', () => {
  beforeEach(() => {
    mockRefresh.mockReset();
  });

  it('disables submit until content is non-empty', () => {
    render(<ReplyForm commentId="c1" />);
    const button = screen.getByRole('button', { name: /post reply/i });
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/^reply$/i), {
      target: { value: 'hello' },
    });
    expect(button).not.toBeDisabled();
  });

  it('calls poster with the trimmed content', async () => {
    const poster = vi.fn(async () => ({ id: 'new' }));
    render(<ReplyForm commentId="c1" poster={poster} />);
    fireEvent.change(screen.getByLabelText(/^reply$/i), {
      target: { value: '  thanks for the feedback  ' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /post reply/i }));
    });
    expect(poster).toHaveBeenCalledWith('c1', 'thanks for the feedback');
    expect(mockRefresh).toHaveBeenCalled();
  });

  it('shows inline alert when poster rejects', async () => {
    const poster = vi.fn(async () => {
      throw new Error('nope');
    });
    render(<ReplyForm commentId="c1" poster={poster} />);
    fireEvent.change(screen.getByLabelText(/^reply$/i), {
      target: { value: 'hi' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /post reply/i }));
    });
    expect(screen.getByRole('alert')).toHaveTextContent(/reply failed/i);
  });

  it('shows a success message after a successful post', async () => {
    const poster = vi.fn(async () => ({ id: 'new' }));
    render(<ReplyForm commentId="c1" poster={poster} />);
    fireEvent.change(screen.getByLabelText(/^reply$/i), {
      target: { value: 'thanks' },
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /post reply/i }));
    });
    expect(screen.getByRole('status')).toHaveTextContent(/reply posted/i);
  });
});
