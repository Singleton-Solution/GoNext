/**
 * DLQ list — Living-Systems brand snapshot.
 *
 * The DLQ list view is an operator surface — calm, dense,
 * instrument-panel feel. This pins three behaviours so a future
 * refactor can't silently regress the brand:
 *
 *  1. Row IDs render in Geist Mono on the brand token surface.
 *  2. Queue cells use the lavender/emerald badge variants from the
 *     brand palette (per pulse.html data-viz tones).
 *  3. Redacted rows pick up the danger-soft chip tint.
 *
 * Assertions look at the Tailwind class string the brand
 * primitives emit — jsdom doesn't apply Tailwind, so the class
 * contract is the proxy for the computed style.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/jobs/dlq',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  listArchivedTasks: vi.fn(),
  replayTask: vi.fn(),
  discardTask: vi.fn(),
  redactTask: vi.fn(),
  getArchivedTask: vi.fn(),
}));
vi.mock('./actions', () => mocks);

import { DLQListClient } from './DLQListClient';
import type { DLQListResponse } from './types';

const sample: DLQListResponse = {
  data: [
    {
      id: 'task-abc-123',
      queue: 'critical',
      type: 'webhook:deliver',
      payload_preview: '{"url":"https://example.com"}',
      last_error: 'timeout',
      failed_at: '2026-05-17T12:00:00Z',
      retried: 3,
      max_retry: 3,
      redacted: false,
    },
    {
      id: 'task-def-456',
      queue: 'webhooks',
      type: 'email:send',
      payload_preview: '{"to":"a@b.c"}',
      last_error: 'SMTP unavailable',
      failed_at: '2026-05-17T11:55:00Z',
      retried: 5,
      max_retry: 5,
      redacted: true,
      redacted_fields: ['to'],
    },
    {
      id: 'task-ghi-789',
      queue: 'low',
      type: 'reports:rollup',
      payload_preview: '{"window":"7d"}',
      last_error: 'pg conn refused',
      failed_at: '2026-05-17T11:50:00Z',
      retried: 1,
      max_retry: 2,
      redacted: false,
    },
  ],
  pagination: { next_cursor: '' },
};

describe('DLQListClient — brand snapshot', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listArchivedTasks.mockResolvedValue(sample);
  });

  it('renders row IDs in Geist Mono (font-mono token)', () => {
    render(<DLQListClient initialQueue="critical" initialData={sample} />);
    const id = screen.getByText('task-abc-123');
    expect(id.className).toContain('font-mono');
    expect(id.className).toContain('text-2xs');
  });

  it('renders the queue cell as a brand Badge with the right tone', () => {
    render(<DLQListClient initialQueue="critical" initialData={sample} />);
    // The "critical" queue → emerald variant.
    const critical = screen.getByText('critical');
    expect(critical.className).toContain('bg-emerald-soft');
    expect(critical.className).toContain('text-emerald-deep');
    // The "webhooks" queue → lavender variant.
    const webhooks = screen.getByText('webhooks');
    expect(webhooks.className).toContain('bg-lavender-soft');
    expect(webhooks.className).toContain('text-lavender-deep');
    // The "low" queue → outline variant.
    const low = screen.getByText('low');
    expect(low.className).toContain('bg-transparent');
  });

  it('redacted rows pick up the danger-soft chip tint on the payload', () => {
    render(<DLQListClient initialQueue="webhooks" initialData={sample} />);
    const redactedChip = screen.getByText(/\(redacted\)/);
    expect(redactedChip.className).toContain('bg-danger-soft');
    expect(redactedChip.className).toContain('text-danger');
  });

  it('non-redacted rows render the payload chip on paper-3', () => {
    render(<DLQListClient initialQueue="critical" initialData={sample} />);
    const chip = screen.getByText('{"url":"https://example.com"}');
    expect(chip.className).toContain('bg-paper-3');
    expect(chip.className).toContain('font-mono');
  });
});
