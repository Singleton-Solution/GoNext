/**
 * FolderTree — unit tests.
 *
 * Covers the parts of the tree that don't depend on a real server:
 *  - Renders a flat list reconstructed into a parent/child tree.
 *  - Clicking a leaf calls `onSelect` with the right id.
 *  - The drop handler reads the custom-MIME payload and calls
 *    `moveMediaToCollection` with the right ids + target.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const mocks = vi.hoisted(() => ({
  listCollections: vi.fn(),
  createCollection: vi.fn(),
  renameCollection: vi.fn(),
  moveCollection: vi.fn(),
  deleteCollection: vi.fn(),
  moveMediaToCollection: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { ALL_NODE_ID, FolderTree, MEDIA_DRAG_MIME, ROOT_NODE_ID } from './FolderTree';
import type { MediaCollection } from '../types';

const c = (id: string, overrides: Partial<MediaCollection> = {}): MediaCollection => ({
  id,
  slug: id,
  name: id.toUpperCase(),
  path: id,
  created_at: '2026-05-17T12:00:00Z',
  updated_at: '2026-05-17T12:00:00Z',
  ...overrides,
});

beforeEach(() => {
  mocks.listCollections.mockReset().mockResolvedValue({ data: [] });
  mocks.moveMediaToCollection.mockReset();
});

describe('FolderTree', () => {
  it('renders an empty tree without crashing', async () => {
    const onSelect = vi.fn();
    render(<FolderTree selectedId={ALL_NODE_ID} onSelect={onSelect} />);
    await waitFor(() => expect(mocks.listCollections).toHaveBeenCalled());
    expect(screen.getByTestId('folder-leaf-all')).toBeInTheDocument();
    expect(screen.getByTestId('folder-leaf-unfiled')).toBeInTheDocument();
  });

  it('reconstructs nested children from a flat list', async () => {
    const marketing = c('marketing', { id: 'p', path: 'marketing' });
    const q1 = c('q1', { id: 'k', path: 'marketing.q1', parent_id: 'p' });
    mocks.listCollections.mockResolvedValueOnce({ data: [marketing, q1] });
    render(<FolderTree selectedId={ALL_NODE_ID} onSelect={vi.fn()} />);
    await waitFor(() => expect(mocks.listCollections).toHaveBeenCalled());
    expect(await screen.findByTestId('folder-row-marketing')).toBeInTheDocument();
    expect(screen.getByTestId('folder-row-q1')).toBeInTheDocument();
  });

  it('fires onSelect with ROOT_NODE_ID when "Unfiled" is clicked', async () => {
    const onSelect = vi.fn();
    render(<FolderTree selectedId={ALL_NODE_ID} onSelect={onSelect} />);
    await waitFor(() => expect(mocks.listCollections).toHaveBeenCalled());
    const unfiled = screen.getByTestId('folder-leaf-unfiled').querySelector('button')!;
    fireEvent.click(unfiled);
    expect(onSelect).toHaveBeenCalledWith(ROOT_NODE_ID);
  });

  it('calls moveMediaToCollection on drop with the dragged ids', async () => {
    const marketing = c('marketing', { id: 'p' });
    mocks.listCollections.mockResolvedValueOnce({ data: [marketing] });
    mocks.moveMediaToCollection.mockResolvedValueOnce({ moved: 2 });
    const onMediaMoved = vi.fn();
    render(
      <FolderTree
        selectedId={ALL_NODE_ID}
        onSelect={vi.fn()}
        onMediaMoved={onMediaMoved}
      />,
    );
    await screen.findByTestId('folder-row-marketing');

    const data = new Map<string, string>();
    data.set(MEDIA_DRAG_MIME, JSON.stringify(['m1', 'm2']));
    const dataTransfer = {
      getData: (k: string) => data.get(k) ?? '',
    };

    const row = screen.getByTestId('folder-row-marketing');
    fireEvent.drop(row, { dataTransfer });

    await waitFor(() => {
      expect(mocks.moveMediaToCollection).toHaveBeenCalledWith({
        ids: ['m1', 'm2'],
        collection_id: 'p',
      });
    });
    await waitFor(() => expect(onMediaMoved).toHaveBeenCalled());
  });

  it('drops onto Unfiled with collection_id null', async () => {
    mocks.listCollections.mockResolvedValueOnce({ data: [] });
    mocks.moveMediaToCollection.mockResolvedValueOnce({ moved: 1 });
    render(<FolderTree selectedId={ALL_NODE_ID} onSelect={vi.fn()} />);
    await waitFor(() => expect(mocks.listCollections).toHaveBeenCalled());

    const data = new Map<string, string>();
    data.set(MEDIA_DRAG_MIME, JSON.stringify(['m1']));
    const dataTransfer = { getData: (k: string) => data.get(k) ?? '' };

    const unfiled = screen.getByTestId('folder-leaf-unfiled');
    fireEvent.drop(unfiled, { dataTransfer });
    await waitFor(() => {
      expect(mocks.moveMediaToCollection).toHaveBeenCalledWith({
        ids: ['m1'],
        collection_id: null,
      });
    });
  });
});
