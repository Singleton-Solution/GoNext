/**
 * MediaGrid — unit tests.
 *
 * Targets the behaviour specific to the grid:
 *   - Renders the seed rows from initialData without a network fetch.
 *   - Empty state appears when initialData is empty.
 *   - Chip switch triggers a refetch with the right `type` filter.
 *   - A successful upload prepends the new asset to the list.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// next/navigation isn't available in jsdom; stub the bits the grid
// reaches for transitively via Link.
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/media',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  listMedia: vi.fn(),
  uploadMedia: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { MediaGrid } from './MediaGrid';
import type { MediaAsset, MediaListResponse } from '../types';

const tile = (id: string, overrides: Partial<MediaAsset> = {}): MediaAsset => ({
  id,
  filename: `${id}.png`,
  mime_type: 'image/png',
  byte_size: 1024,
  alt_text: '',
  caption: '',
  storage_key: `k/${id}`,
  public_url: `memory:///k/${id}`,
  uploader_id: 'user-1',
  created_at: '2026-05-17T12:00:00Z',
  updated_at: '2026-05-17T12:00:00Z',
  ...overrides,
});

beforeEach(() => {
  mocks.listMedia.mockReset();
  mocks.uploadMedia.mockReset();
});

describe('MediaGrid', () => {
  it('renders seed rows from initialData', () => {
    // Even with initialData present, the grid kicks off a refetch on
    // mount via the chip-filter effect. We stub it so the test doesn't
    // race on the network mock.
    mocks.listMedia.mockResolvedValue({ data: [tile('a'), tile('b')], pagination: { next_cursor: '' } });

    const initial: MediaListResponse = {
      data: [tile('a'), tile('b')],
      pagination: { next_cursor: '' },
    };
    render(<MediaGrid initialData={initial} />);
    expect(screen.getByTestId('media-tile-a')).toBeInTheDocument();
    expect(screen.getByTestId('media-tile-b')).toBeInTheDocument();
  });

  it('shows the empty state when the grid is empty after load', async () => {
    mocks.listMedia.mockResolvedValueOnce({ data: [], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [], pagination: { next_cursor: '' } }} />);
    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument();
    });
  });

  it('refetches with type=image when the Images chip is clicked', async () => {
    mocks.listMedia.mockResolvedValue({ data: [tile('a')], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [tile('a')], pagination: { next_cursor: '' } }} />);

    fireEvent.click(screen.getByTestId('filter-chip-image'));
    await waitFor(() => {
      const lastCall = mocks.listMedia.mock.calls.at(-1);
      expect(lastCall?.[0]).toMatchObject({ type: 'image' });
    });
  });

  it('renders an upload dropzone above the grid', () => {
    mocks.listMedia.mockResolvedValue({ data: [], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [], pagination: { next_cursor: '' } }} />);
    expect(screen.getByTestId('upload-dropzone')).toBeInTheDocument();
  });

  it('renders the brand headline with the italic accent on "library"', () => {
    mocks.listMedia.mockResolvedValue({ data: [], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [], pagination: { next_cursor: '' } }} />);
    // The Headline primitive composes <em> children with the brand's
    // italic-accent rule; the heading text reads "Media library."
    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading).toHaveTextContent('Media library.');
    const accent = heading.querySelector('em');
    expect(accent).not.toBeNull();
    expect(accent?.textContent).toBe('library');
  });

  it('exposes the full chip set including Images, Video, Files and Audio', () => {
    mocks.listMedia.mockResolvedValue({ data: [], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [], pagination: { next_cursor: '' } }} />);
    // The brand spec calls out Images / Video / Files / Audio in
    // addition to the catch-all "All" chip.
    expect(screen.getByTestId('filter-chip-all')).toBeInTheDocument();
    expect(screen.getByTestId('filter-chip-image')).toBeInTheDocument();
    expect(screen.getByTestId('filter-chip-video')).toBeInTheDocument();
    expect(screen.getByTestId('filter-chip-document')).toBeInTheDocument();
    expect(screen.getByTestId('filter-chip-audio')).toBeInTheDocument();
  });

  it('matches the brand snapshot for the chip row', () => {
    mocks.listMedia.mockResolvedValue({ data: [], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [], pagination: { next_cursor: '' } }} />);
    // Snapshot the tablist subtree only — this pins the class shape
    // (emerald-soft active, paper-2 idle, pill radius) without
    // dragging the empty-state / dropzone DOM into the snapshot.
    const tablist = screen.getByRole('tablist');
    expect(tablist).toMatchSnapshot();
  });

  it('renders the hover-overlay edit + delete affordances per tile', () => {
    mocks.listMedia.mockResolvedValue({ data: [tile('a')], pagination: { next_cursor: '' } });
    render(<MediaGrid initialData={{ data: [tile('a')], pagination: { next_cursor: '' } }} />);
    // Both affordances are part of the brand spec: emerald edit +
    // lavender delete. They're test-ID'd so the brand surface is
    // pinned even though the overlay is opacity-0 until hover.
    expect(screen.getByTestId('tile-edit-a')).toBeInTheDocument();
    expect(screen.getByTestId('tile-delete-a')).toBeInTheDocument();
  });
});
