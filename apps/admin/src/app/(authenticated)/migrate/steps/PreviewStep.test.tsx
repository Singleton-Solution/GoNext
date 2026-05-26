/**
 * PreviewStep — unit tests.
 *
 * Covers:
 *   - the synthetic-fallback path when the API errors out
 *   - that the auto-fetch fires on mount
 *   - that buildDryRunForm packs the FormData correctly
 *
 * Real network behaviour is exercised by the Playwright happy-path
 * test under tools/e2e (when the API is available).
 */
import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { PreviewStep, buildDryRunForm, syntheticPreview } from './PreviewStep';
import { DEFAULT_OPTIONS } from '../types';

const failFetcher: typeof fetch = () =>
  Promise.reject(new Error('test: no network'));

describe('PreviewStep', () => {
  it('auto-fetches on mount and falls back to demo data on error', async () => {
    const onValue = vi.fn();
    render(
      <PreviewStep
        source={{ kind: 'wxr-upload', wxrFile: new File(['x'], 'x.xml') }}
        options={DEFAULT_OPTIONS}
        value={null}
        onValue={onValue}
        fetcher={failFetcher}
        onBack={vi.fn()}
        onNext={vi.fn()}
        onError={vi.fn()}
      />,
    );
    await waitFor(() => {
      expect(onValue).toHaveBeenCalled();
    });
    // The last call carries the synthetic preview.
    const last = onValue.mock.calls.at(-1)?.[0];
    expect(last).toEqual(
      expect.objectContaining({ warnings: expect.any(Array) }),
    );
  });

  it('renders count tiles for a non-null value', () => {
    render(
      <PreviewStep
        source={{ kind: 'wxr-upload', wxrFile: new File(['x'], 'x.xml') }}
        options={DEFAULT_OPTIONS}
        value={{
          authors: 5,
          categories: 3,
          tags: 7,
          posts: 42,
          attachments: 4,
          comments: 11,
          warnings: ['demo warning'],
        }}
        onValue={vi.fn()}
        fetcher={failFetcher}
        onBack={vi.fn()}
        onNext={vi.fn()}
        onError={vi.fn()}
      />,
    );
    expect(screen.getByTestId('preview-counts')).toBeInTheDocument();
    expect(screen.getByTestId('preview-warnings')).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
  });
});

describe('buildDryRunForm', () => {
  it('packs a WXR upload', () => {
    const file = new File(['x'], 'a.xml');
    const fd = buildDryRunForm(
      { kind: 'wxr-upload', wxrFile: file },
      DEFAULT_OPTIONS,
    );
    expect(fd.get('kind')).toBe('wxr-upload');
    expect(fd.get('file')).toBe(file);
    expect(fd.get('options')).toBeTruthy();
  });

  it('packs a REST URL', () => {
    const fd = buildDryRunForm(
      { kind: 'rest-url', restUrl: 'https://blog.example.com' },
      DEFAULT_OPTIONS,
    );
    expect(fd.get('restUrl')).toBe('https://blog.example.com');
    expect(fd.get('file')).toBeNull();
  });

  it('packs an ACF path', () => {
    const fd = buildDryRunForm(
      { kind: 'acf-json', acfPath: '/etc/acf.json' },
      DEFAULT_OPTIONS,
    );
    expect(fd.get('acfPath')).toBe('/etc/acf.json');
  });
});

describe('syntheticPreview', () => {
  it('scales WXR counts with file size', () => {
    const small = syntheticPreview({
      kind: 'wxr-upload',
      wxrFile: new File([new ArrayBuffer(0)], 'tiny.xml'),
    });
    const large = syntheticPreview({
      kind: 'wxr-upload',
      wxrFile: new File([new ArrayBuffer(10 * 1024 * 1024)], 'big.xml'),
    });
    expect(large.posts).toBeGreaterThan(small.posts);
  });

  it('produces zero posts for ACF JSON', () => {
    const r = syntheticPreview({ kind: 'acf-json', acfPath: '/x' });
    expect(r.posts).toBe(0);
    expect(r.warnings.length).toBeGreaterThan(0);
  });
});
