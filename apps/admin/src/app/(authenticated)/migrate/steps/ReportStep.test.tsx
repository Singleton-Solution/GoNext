/**
 * ReportStep — unit tests.
 *
 * Verifies that the report's three terminal states (succeeded,
 * partial, failed) render distinct banners, that errors paginate,
 * and that "Start over" calls onRestart.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('next/link', () => ({
  default: ({ children, href }: { children: React.ReactNode; href: string }) =>
    <a href={href}>{children}</a>,
}));

import { ReportStep } from './ReportStep';
import type { RunStatus } from '../types';

const baseStatus: RunStatus = {
  jobId: 'abc',
  status: 'done',
  percent: 100,
  phase: 'done',
  counts: {
    authors: 1,
    categories: 1,
    tags: 0,
    posts: 5,
    attachments: 2,
    comments: 3,
    warnings: [],
  },
  errors: [],
};

describe('ReportStep', () => {
  it('renders empty state when runStatus is null', () => {
    render(<ReportStep runStatus={null} onRestart={vi.fn()} />);
    expect(screen.getByTestId('report-empty')).toBeInTheDocument();
  });

  it('renders success banner for clean run', () => {
    render(<ReportStep runStatus={baseStatus} onRestart={vi.fn()} />);
    const banner = screen.getByTestId('report-banner');
    expect(banner.className).toMatch(/emerald/);
  });

  it('renders warning banner for partial run', () => {
    render(
      <ReportStep
        runStatus={{ ...baseStatus, errors: ['post X: bad date'] }}
        onRestart={vi.fn()}
      />,
    );
    const banner = screen.getByTestId('report-banner');
    expect(banner.className).toMatch(/warn/);
    expect(screen.getByTestId('report-errors')).toBeInTheDocument();
  });

  it('renders danger banner for failed run', () => {
    render(
      <ReportStep
        runStatus={{ ...baseStatus, status: 'failed' }}
        onRestart={vi.fn()}
      />,
    );
    const banner = screen.getByTestId('report-banner');
    expect(banner.className).toMatch(/danger/);
  });

  it('calls onRestart when Start over is clicked', () => {
    const onRestart = vi.fn();
    render(<ReportStep runStatus={baseStatus} onRestart={onRestart} />);
    fireEvent.click(screen.getByTestId('report-restart'));
    expect(onRestart).toHaveBeenCalledOnce();
  });

  it('truncates error list past 50 entries', () => {
    const many = Array.from({ length: 75 }, (_, i) => `err ${i}`);
    render(
      <ReportStep
        runStatus={{ ...baseStatus, errors: many }}
        onRestart={vi.fn()}
      />,
    );
    expect(screen.getByText(/and 25 more/)).toBeInTheDocument();
  });
});
