/**
 * Tests for the dashboard QuickDraftCard widget. We pin three flows:
 *
 *   1. Successful POST clears the form and shows the inline success chip.
 *   2. Server-side error surfaces in the danger chip with the API message.
 *   3. Submitting an empty form refuses the request and shows a hint.
 *
 * The fetch wrapper (`apps/admin/src/lib/api-client`) is mocked so the
 * tests don't go anywhere near the network.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const postMock = vi.fn();

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>(
    '@/lib/api-client',
  );
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: (...args: unknown[]) => postMock(...args),
      put: vi.fn(),
      patch: vi.fn(),
      delete: vi.fn(),
    },
  };
});

import { ApiError } from '@/lib/api-client';
import { QuickDraftCard } from './QuickDraftCard';

describe('QuickDraftCard', () => {
  beforeEach(() => {
    postMock.mockReset();
  });

  it('posts a draft with the expected payload and clears the form', async () => {
    postMock.mockResolvedValueOnce({ id: 'p_123' });

    render(<QuickDraftCard />);

    fireEvent.change(screen.getByTestId('quick-draft-title'), {
      target: { value: 'Brew journal' },
    });
    fireEvent.change(screen.getByTestId('quick-draft-content'), {
      target: { value: 'Today I tried a 1:16 ratio.\n\nNext: try 1:17.' },
    });
    fireEvent.submit(screen.getByTestId('quick-draft-form'));

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    expect(postMock).toHaveBeenCalledWith('/api/v1/posts', {
      status: 'draft',
      title: 'Brew journal',
      content_blocks: {
        version: 1,
        blocks: [
          { type: 'core/paragraph', attributes: { content: 'Today I tried a 1:16 ratio.' } },
          { type: 'core/paragraph', attributes: { content: 'Next: try 1:17.' } },
        ],
      },
    });

    await waitFor(() =>
      expect(screen.getByTestId('quick-draft-status').textContent).toMatch(
        /Draft saved/,
      ),
    );
    expect(
      (screen.getByTestId('quick-draft-title') as HTMLInputElement).value,
    ).toBe('');
    expect(
      (screen.getByTestId('quick-draft-content') as HTMLTextAreaElement).value,
    ).toBe('');
  });

  it('surfaces the server error message on failure', async () => {
    postMock.mockRejectedValueOnce(
      new ApiError(422, 'Unprocessable Entity', {
        error: { code: 'invalid', message: 'Title too short.' },
      }),
    );

    render(<QuickDraftCard />);

    fireEvent.change(screen.getByTestId('quick-draft-title'), {
      target: { value: 'X' },
    });
    fireEvent.submit(screen.getByTestId('quick-draft-form'));

    await waitFor(() =>
      expect(screen.getByTestId('quick-draft-status').textContent).toMatch(
        /Title too short/,
      ),
    );
  });

  it('refuses an empty form before hitting the API', () => {
    render(<QuickDraftCard />);
    fireEvent.submit(screen.getByTestId('quick-draft-form'));

    expect(postMock).not.toHaveBeenCalled();
    expect(screen.getByTestId('quick-draft-status').textContent).toMatch(
      /Add a title or some content/,
    );
  });
});
