/**
 * UploadDropzone — unit tests.
 *
 * The dropzone has three behaviours worth pinning:
 *   - The click-to-pick fallback fires uploadMedia with the chosen file.
 *   - A successful upload bubbles via the onUploaded callback.
 *   - A failed upload renders the server-supplied detail message in
 *     a role="alert" panel next to the offending file.
 *
 * We stub the actions module so no real fetch escapes the test.
 * Drag-and-drop is harder to fake in jsdom (the DataTransfer object
 * is partial), so we exercise the same code path via the file input.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const mocks = vi.hoisted(() => ({
  uploadMedia: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { UploadDropzone } from './UploadDropzone';
import { ApiError } from '@/lib/api-client';
import type { MediaAsset } from '../types';

const sampleAsset: MediaAsset = {
  id: 'asset-1',
  filename: 'logo.png',
  mime_type: 'image/png',
  byte_size: 100,
  alt_text: '',
  caption: '',
  storage_key: '2026/05/logo.png',
  public_url: 'memory:///2026/05/logo.png',
  uploader_id: 'user-1',
  created_at: '2026-05-17T12:00:00Z',
  updated_at: '2026-05-17T12:00:00Z',
};

beforeEach(() => {
  mocks.uploadMedia.mockReset();
});

describe('UploadDropzone', () => {
  it('uploads a chosen file and bubbles the asset', async () => {
    mocks.uploadMedia.mockResolvedValueOnce(sampleAsset);
    const onUploaded = vi.fn();
    render(<UploadDropzone onUploaded={onUploaded} />);

    const file = new File(['fake-png-bytes'], 'logo.png', { type: 'image/png' });
    const input = screen.getByTestId('upload-file-input') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });

    await waitFor(() => {
      expect(mocks.uploadMedia).toHaveBeenCalledTimes(1);
    });
    const [calledFile] = mocks.uploadMedia.mock.calls[0]!;
    expect((calledFile as File).name).toBe('logo.png');

    await waitFor(() => {
      expect(onUploaded).toHaveBeenCalledWith(sampleAsset);
    });
  });

  it('renders the server detail message when the upload fails', async () => {
    mocks.uploadMedia.mockRejectedValueOnce(
      new ApiError(415, 'Unsupported Media Type', {
        type: 'about:blank',
        title: 'Unsupported Media Type',
        status: 415,
        code: 'unsupported_media',
        detail: 'file type "application/x-msdownload" is not allowed',
      }),
    );
    const onUploaded = vi.fn();
    render(<UploadDropzone onUploaded={onUploaded} />);

    const file = new File(['x'], 'evil.exe', { type: 'application/octet-stream' });
    const input = screen.getByTestId('upload-file-input') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toMatch(/not allowed/);
    expect(onUploaded).not.toHaveBeenCalled();
  });

  it('renders a progress row per file', async () => {
    // Pending promise — never resolves, so we see the "uploading" row.
    mocks.uploadMedia.mockImplementation(
      () => new Promise(() => {}),
    );
    render(<UploadDropzone onUploaded={vi.fn()} />);

    const file1 = new File(['a'], 'a.png', { type: 'image/png' });
    const file2 = new File(['b'], 'b.png', { type: 'image/png' });
    const input = screen.getByTestId('upload-file-input') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file1, file2] } });

    await waitFor(() => {
      expect(screen.getByText('a.png')).toBeInTheDocument();
      expect(screen.getByText('b.png')).toBeInTheDocument();
    });
  });

  it('matches the brand snapshot for the resting dropzone surface', () => {
    // Snapshot the empty resting state — paper-3 surface, dashed
    // border-strong, UploadCloud glyph, choose-a-file button. Pins
    // the class shape against accidental token drift.
    const { container } = render(<UploadDropzone onUploaded={vi.fn()} />);
    expect(container.querySelector('[data-testid="upload-dropzone"]')).toMatchSnapshot();
  });
});
