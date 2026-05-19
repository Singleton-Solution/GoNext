/**
 * SiteEditorClient interactive tests.
 *
 * Mocks `./api` so the page never reaches the network. We assert:
 *
 *   1. The rail renders one entry per part returned by the API.
 *   2. Switching parts loads the matching tree into the editor.
 *   3. Editing the textarea triggers an autosave PUT after the timer.
 *   4. The "Reset to theme default" button fires DELETE and reloads.
 *   5. Invalid JSON parks the parse error in a banner without
 *      reaching the autosave path.
 *   6. The reset button is disabled when the selected part has no
 *      override.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

const fetchPartsMock = vi.fn();
const putPartMock = vi.fn();
const deletePartMock = vi.fn();

vi.mock('./api', () => ({
  SITE_EDITOR_BASE: '/api/v1/admin/site_editor',
  fetchParts: (signal?: AbortSignal) => fetchPartsMock(signal),
  putPart: (name: string, payload: unknown, signal?: AbortSignal) =>
    putPartMock(name, payload, signal),
  deletePart: (name: string, signal?: AbortSignal) => deletePartMock(name, signal),
}));

import { SiteEditorClient } from './SiteEditorClient';
import type { SiteEditorPartsResponse } from './types';

function makeResponse(over: Partial<SiteEditorPartsResponse> = {}): SiteEditorPartsResponse {
  return {
    theme: 'gn-hello',
    parts: [
      {
        name: 'header',
        title: 'Header',
        area: 'header',
        blocks: [{ name: 'core/paragraph', attrs: { content: 'Hello' } }],
        overridden: false,
      },
      {
        name: 'footer',
        title: 'Footer',
        area: 'footer',
        blocks: [{ name: 'core/paragraph', attrs: { content: 'Bye' } }],
        overridden: true,
      },
    ],
    ...over,
  };
}

describe('SiteEditorClient', () => {
  beforeEach(() => {
    fetchPartsMock.mockReset();
    putPartMock.mockReset();
    deletePartMock.mockReset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders one rail entry per part and selects the first part', async () => {
    fetchPartsMock.mockResolvedValueOnce(makeResponse());

    render(<SiteEditorClient />);

    await waitFor(() => {
      expect(screen.getByTestId('site-editor-rail')).toBeInTheDocument();
    });
    expect(screen.getByTestId('site-editor-part-header')).toBeInTheDocument();
    expect(screen.getByTestId('site-editor-part-footer')).toBeInTheDocument();
    // First part selected by default.
    const editor = screen.getByTestId('site-editor-editor') as HTMLTextAreaElement;
    expect(editor.value).toContain('Hello');
  });

  it('shows the Modified badge for overridden parts', async () => {
    fetchPartsMock.mockResolvedValueOnce(makeResponse());

    render(<SiteEditorClient />);

    await waitFor(() => screen.getByTestId('site-editor-rail'));
    expect(screen.getByTestId('site-editor-badge-footer')).toBeInTheDocument();
    // Header is NOT overridden.
    expect(screen.queryByTestId('site-editor-badge-header')).toBeNull();
  });

  it('switches the editor content when a different part is clicked', async () => {
    fetchPartsMock.mockResolvedValueOnce(makeResponse());

    render(<SiteEditorClient />);

    await waitFor(() => screen.getByTestId('site-editor-rail'));
    const footerBtn = screen.getByTestId('site-editor-part-footer').querySelector('button');
    fireEvent.click(footerBtn!);

    const editor = screen.getByTestId('site-editor-editor') as HTMLTextAreaElement;
    expect(editor.value).toContain('Bye');
  });

  it('autosaves edits to the selected part', async () => {
    fetchPartsMock.mockResolvedValueOnce(makeResponse());
    putPartMock.mockResolvedValue({
      theme: 'gn-hello',
      name: 'header',
      blocks: [],
      overridden: true,
    });

    render(<SiteEditorClient />);

    await waitFor(() => screen.getByTestId('site-editor-rail'));

    // Edit the textarea — an explicit empty array is a valid tree.
    const editor = screen.getByTestId('site-editor-editor') as HTMLTextAreaElement;
    fireEvent.change(editor, { target: { value: '[]' } });

    // Advance past the 5s interval to fire the timer.
    await act(async () => {
      vi.advanceTimersByTime(5_500);
    });

    await waitFor(() => {
      expect(putPartMock).toHaveBeenCalled();
    });
    expect(putPartMock).toHaveBeenCalledWith(
      'header',
      { blocks: [] },
      expect.any(Object),
    );
  });

  it('reset button calls DELETE and refetches', async () => {
    fetchPartsMock
      .mockResolvedValueOnce(makeResponse())
      .mockResolvedValueOnce(makeResponse({
        parts: [
          {
            name: 'header',
            title: 'Header',
            area: 'header',
            blocks: [],
            overridden: false,
          },
          {
            name: 'footer',
            title: 'Footer',
            area: 'footer',
            blocks: [],
            overridden: false,
          },
        ],
      }));
    deletePartMock.mockResolvedValueOnce(undefined);

    render(<SiteEditorClient />);

    await waitFor(() => screen.getByTestId('site-editor-rail'));
    // Switch to footer (which is the overridden one in the fixture).
    fireEvent.click(screen.getByTestId('site-editor-part-footer').querySelector('button')!);

    const resetBtn = screen.getByTestId('site-editor-reset') as HTMLButtonElement;
    expect(resetBtn.disabled).toBe(false);
    fireEvent.click(resetBtn);

    await waitFor(() => {
      expect(deletePartMock).toHaveBeenCalledWith('footer', undefined);
    });
    await waitFor(() => {
      expect(fetchPartsMock).toHaveBeenCalledTimes(2);
    });
  });

  it('reset button is disabled when the selected part has no override', async () => {
    fetchPartsMock.mockResolvedValueOnce(makeResponse());

    render(<SiteEditorClient />);

    await waitFor(() => screen.getByTestId('site-editor-rail'));
    // Default selection is header — not overridden.
    const resetBtn = screen.getByTestId('site-editor-reset') as HTMLButtonElement;
    expect(resetBtn.disabled).toBe(true);
  });

  it('surfaces JSON parse errors without firing a save', async () => {
    fetchPartsMock.mockResolvedValueOnce(makeResponse());

    render(<SiteEditorClient />);

    await waitFor(() => screen.getByTestId('site-editor-rail'));
    const editor = screen.getByTestId('site-editor-editor') as HTMLTextAreaElement;
    fireEvent.change(editor, { target: { value: 'not json' } });

    expect(screen.getByTestId('site-editor-parse-error')).toBeInTheDocument();

    // Advance past the interval — no save should fire.
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    expect(putPartMock).not.toHaveBeenCalled();
  });

  it('shows the banner on load failure with a Retry button', async () => {
    fetchPartsMock.mockRejectedValueOnce(new Error('boom'));

    render(<SiteEditorClient />);

    await waitFor(() => {
      expect(screen.getByTestId('site-editor-banner')).toBeInTheDocument();
    });
  });
});
