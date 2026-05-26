/**
 * MigrationWizard — orchestration tests.
 *
 * We unit-test the wizard's state machine and the per-step transitions.
 * The step components have their own focused tests in steps/*.test.tsx;
 * this file proves they wire together correctly.
 *
 * jsdom doesn't run a real network, so the fetcher used by the
 * preview / run steps is stubbed to return canned data. We rely on
 * the synthetic-fallback path in those steps for behaviour the test
 * can pin without relying on the actual API.
 */
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

// next/link is a server component in production; we stub it for jsdom.
vi.mock('next/link', () => ({
  default: ({ children, href }: { children: React.ReactNode; href: string }) =>
    <a href={href}>{children}</a>,
}));

import { MigrationWizard } from './MigrationWizard';

/** A fetcher that always errors so the wizard takes the synthetic
 * fallback path. Lets us drive the state machine without modelling a
 * real server. */
const failFetcher: typeof fetch = () =>
  Promise.reject(new Error('test: no network'));

describe('MigrationWizard', () => {
  it('renders the stepper with five markers and starts on source', () => {
    render(<MigrationWizard fetcher={failFetcher} />);
    expect(screen.getByTestId('step-marker-source')).toBeInTheDocument();
    expect(screen.getByTestId('step-marker-options')).toBeInTheDocument();
    expect(screen.getByTestId('step-marker-preview')).toBeInTheDocument();
    expect(screen.getByTestId('step-marker-run')).toBeInTheDocument();
    expect(screen.getByTestId('step-marker-report')).toBeInTheDocument();
    // Source step is current.
    expect(screen.getByText('1. Source')).toBeInTheDocument();
  });

  it('advances through source → options when a WXR file is supplied', () => {
    render(<MigrationWizard fetcher={failFetcher} />);
    // Continue is disabled until a file is supplied.
    const next = screen.getByTestId('source-next');
    expect(next).toBeDisabled();
    // Simulate a file selection.
    const file = new File(['<rss />'], 'test.xml', { type: 'application/xml' });
    const fileInput = screen.getByTestId('source-wxr-file') as HTMLInputElement;
    fireEvent.change(fileInput, { target: { files: [file] } });
    expect(next).not.toBeDisabled();
    fireEvent.click(next);
    expect(screen.getByText('2. Options')).toBeInTheDocument();
  });

  it('back from options returns to source preserving state', () => {
    render(<MigrationWizard fetcher={failFetcher} />);
    const file = new File(['<rss />'], 'a.xml', { type: 'application/xml' });
    fireEvent.change(screen.getByTestId('source-wxr-file'), { target: { files: [file] } });
    fireEvent.click(screen.getByTestId('source-next'));
    fireEvent.click(screen.getByTestId('options-back'));
    expect(screen.getByText('1. Source')).toBeInTheDocument();
    // File was preserved across navigation.
    expect(screen.getByText(/a\.xml/)).toBeInTheDocument();
  });

  it('shows preview counts after auto-fetch falls through to demo data', async () => {
    render(<MigrationWizard fetcher={failFetcher} />);
    const file = new File(['<rss />'], 'b.xml', { type: 'application/xml' });
    fireEvent.change(screen.getByTestId('source-wxr-file'), { target: { files: [file] } });
    fireEvent.click(screen.getByTestId('source-next'));
    fireEvent.click(screen.getByTestId('options-next'));
    // Preview auto-fetches on mount and falls back to demo data.
    await waitFor(() => screen.getByTestId('preview-counts'));
    expect(screen.getByTestId('preview-counts')).toBeInTheDocument();
  });

  it('navigates options -> preview -> run after a file is supplied', async () => {
    render(<MigrationWizard fetcher={failFetcher} />);
    const file = new File(['<rss />'], 'c.xml', { type: 'application/xml' });
    fireEvent.change(screen.getByTestId('source-wxr-file'), { target: { files: [file] } });
    fireEvent.click(screen.getByTestId('source-next'));
    fireEvent.click(screen.getByTestId('options-next'));
    // Preview auto-fetches; the synthetic fallback resolves quickly.
    await waitFor(() => screen.getByTestId('preview-next'));
    fireEvent.click(screen.getByTestId('preview-next'));
    // Run step renders progress bar.
    await waitFor(() => screen.getByTestId('run-progressbar'));
    expect(screen.getByTestId('run-progressbar')).toBeInTheDocument();
  });

  it('start over from the report rewinds to source', async () => {
    // Render the wizard already on the report step by driving the state
    // machine forward via state directly is not exposed; instead we walk
    // up to run and then manually advance via the run-next click once the
    // synthetic simulator completes. To keep this test fast, the
    // synthetic timer in RunStep is exercised via real timers and a
    // longer wait.
    render(<MigrationWizard fetcher={failFetcher} />);
    const file = new File(['<rss />'], 'd.xml', { type: 'application/xml' });
    fireEvent.change(screen.getByTestId('source-wxr-file'), { target: { files: [file] } });
    fireEvent.click(screen.getByTestId('source-next'));
    fireEvent.click(screen.getByTestId('options-next'));
    await waitFor(() => screen.getByTestId('preview-next'));
    fireEvent.click(screen.getByTestId('preview-next'));
    // The synthetic simulator advances 10% every 500ms = ~5.5s to 100%.
    await waitFor(
      () => {
        const btn = screen.getByTestId('run-next') as HTMLButtonElement;
        expect(btn.disabled).toBe(false);
      },
      { timeout: 8_000 },
    );
    fireEvent.click(screen.getByTestId('run-next'));
    expect(screen.getByText('5. Report')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('report-restart'));
    expect(screen.getByText('1. Source')).toBeInTheDocument();
    expect(screen.getByTestId('source-next')).toBeDisabled();
  }, 15_000);
});
