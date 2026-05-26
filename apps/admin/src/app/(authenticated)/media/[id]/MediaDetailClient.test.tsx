/**
 * MediaDetailClient — unit tests.
 *
 * The editor has two side-effecting actions worth pinning:
 *   - PATCH updateMedia on submit; local state reflects the response.
 *   - DELETE deleteMedia + router.push back to the grid on confirm.
 *
 * Also exercises the display of the storage URL — it's the primary
 * "give me the link" surface for the operator and we want a
 * regression-resistant test that the panel renders.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const routerMock = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
vi.mock('next/navigation', () => ({
  useRouter: () => routerMock,
  usePathname: () => '/media/asset-1',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  updateMedia: vi.fn(),
  deleteMedia: vi.fn(),
  getMedia: vi.fn(),
  listMedia: vi.fn(),
  uploadMedia: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { MediaDetailClient } from './MediaDetailClient';
import type { MediaAsset } from '../types';

const asset: MediaAsset = {
  id: 'asset-1',
  filename: 'logo.png',
  mime_type: 'image/png',
  byte_size: 1024,
  alt_text: 'original alt',
  caption: 'original caption',
  storage_key: 'k/logo.png',
  public_url: 'memory:///k/logo.png',
  uploader_id: 'user-1',
  created_at: '2026-05-17T12:00:00Z',
  updated_at: '2026-05-17T12:00:00Z',
  tags: [],
};

beforeEach(() => {
  mocks.updateMedia.mockReset();
  mocks.deleteMedia.mockReset();
  routerMock.push.mockReset();
});

describe('MediaDetailClient', () => {
  it('renders the initial alt-text and caption', () => {
    render(<MediaDetailClient initial={asset} />);
    expect((screen.getByTestId('alt-text-input') as HTMLTextAreaElement).value).toBe(
      'original alt',
    );
    expect((screen.getByTestId('caption-input') as HTMLTextAreaElement).value).toBe(
      'original caption',
    );
  });

  it('renders the storage URL panel', () => {
    render(<MediaDetailClient initial={asset} />);
    const link = screen.getByRole('link', { name: /memory:\/\/\/k\/logo.png/ });
    expect(link).toBeInTheDocument();
  });

  it('saves alt-text changes via PATCH', async () => {
    mocks.updateMedia.mockResolvedValueOnce({
      ...asset,
      alt_text: 'new alt',
      updated_at: '2026-05-18T12:00:00Z',
    });
    render(<MediaDetailClient initial={asset} />);

    const input = screen.getByTestId('alt-text-input');
    fireEvent.change(input, { target: { value: 'new alt' } });
    fireEvent.click(screen.getByTestId('save-button'));

    await waitFor(() => {
      expect(mocks.updateMedia).toHaveBeenCalledWith('asset-1', {
        alt_text: 'new alt',
        caption: 'original caption',
      });
    });
    expect(await screen.findByTestId('save-confirmation')).toBeInTheDocument();
  });

  it('soft-deletes and redirects back to the library on confirm', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    mocks.deleteMedia.mockResolvedValueOnce(undefined);
    render(<MediaDetailClient initial={asset} />);

    fireEvent.click(screen.getByTestId('delete-button'));
    await waitFor(() => {
      expect(mocks.deleteMedia).toHaveBeenCalledWith('asset-1');
    });
    await waitFor(() => {
      expect(routerMock.push).toHaveBeenCalledWith('/media');
    });
    confirmSpy.mockRestore();
  });

  it('does NOT delete when the operator cancels the confirm dialog', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
    render(<MediaDetailClient initial={asset} />);

    fireEvent.click(screen.getByTestId('delete-button'));
    // No need to await — the cancel path is synchronous.
    expect(mocks.deleteMedia).not.toHaveBeenCalled();
    confirmSpy.mockRestore();
  });

  it('exposes a copy-to-clipboard button on the storage URL panel', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    render(<MediaDetailClient initial={asset} />);

    const copy = screen.getByTestId('copy-url-button');
    fireEvent.click(copy);
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith('memory:///k/logo.png');
    });
  });

  it('renders the EXIF / metadata table with mono values', () => {
    render(<MediaDetailClient initial={asset} />);
    const meta = screen.getByTestId('media-detail-metadata');
    // The mime type and storage key appear in the table body — they
    // live in <dd> nodes with the brand's font-mono class. Asserting
    // textContent (rather than a className-coupled assertion) keeps
    // the test resistant to Tailwind output changes.
    expect(meta.textContent).toContain('image/png');
    expect(meta.textContent).toContain('k/logo.png');
  });

  it('matches the brand snapshot for the storage URL panel', () => {
    render(<MediaDetailClient initial={asset} />);
    expect(screen.getByTestId('media-detail-storage')).toMatchSnapshot();
  });

  it('hides the derivatives panel when no derivatives are present', () => {
    render(<MediaDetailClient initial={asset} />);
    expect(screen.queryByTestId('media-detail-derivatives')).not.toBeInTheDocument();
  });

  it('renders the HLS link when hls_url is set (issue #52)', () => {
    const video: MediaAsset = {
      ...asset,
      id: 'video-1',
      mime_type: 'video/mp4',
      filename: 'clip.mp4',
      hls_url: 'https://cdn.example/hls/video-1/index.m3u8',
    };
    render(<MediaDetailClient initial={video} />);
    const link = screen.getByTestId('hls-link');
    expect(link).toHaveAttribute('href', 'https://cdn.example/hls/video-1/index.m3u8');
  });

  it('renders the extracted-text link when has_extracted_text is true (issue #60)', () => {
    const pdf: MediaAsset = {
      ...asset,
      id: 'pdf-1',
      mime_type: 'application/pdf',
      filename: 'doc.pdf',
      has_extracted_text: true,
    };
    render(<MediaDetailClient initial={pdf} />);
    const link = screen.getByTestId('extracted-text-link');
    expect(link).toHaveAttribute('href', '/media/pdf-1/text');
  });

  it('renders the proxy source link for proxied assets (issue #187)', () => {
    const proxied: MediaAsset = {
      ...asset,
      id: 'proxy-1',
      is_proxied: true,
      source_url: 'https://oldsite.example/uploads/2024/03/photo.jpg',
    };
    render(<MediaDetailClient initial={proxied} />);
    const link = screen.getByTestId('proxy-source-link');
    expect(link).toHaveAttribute(
      'href',
      'https://oldsite.example/uploads/2024/03/photo.jpg',
    );
  });
});
