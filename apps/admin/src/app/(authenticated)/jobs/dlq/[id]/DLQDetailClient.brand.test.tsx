/**
 * DLQ detail — Living-Systems brand snapshot.
 *
 * Pins the visual contract:
 *  1. Headline renders the italic-accent rule ("Failure on *task.type*").
 *  2. The action toolbar uses the emerald/danger/lavender colour cues:
 *     Replay → emerald, Discard → danger (destructive), Redact →
 *     lavender accent.
 *  3. Payload JSON viewer sits on paper-3 (Geist Mono, recessed).
 *  4. The last-error trace also sits on paper-3 mono.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => '/jobs/dlq/abc',
  useSearchParams: () => new URLSearchParams(),
}));

const mocks = vi.hoisted(() => ({
  replayTask: vi.fn(),
  discardTask: vi.fn(),
  redactTask: vi.fn(),
  getArchivedTask: vi.fn(),
}));
vi.mock('../actions', () => mocks);

import { DLQDetailClient } from './DLQDetailClient';
import type { ArchivedTask } from '../types';

const sample: ArchivedTask = {
  id: 'task-abc-123',
  queue: 'webhooks',
  type: 'webhook:deliver',
  payload: '{"url":"https://example.com","token":"abcd"}',
  payload_preview: '{"url":"https://example.com"}',
  last_error: 'remote returned 500',
  failed_at: '2026-05-17T12:00:00Z',
  retried: 3,
  max_retry: 3,
  redacted: false,
};

describe('DLQDetailClient — brand snapshot', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the headline with the italic-accent rule on the task type', () => {
    render(<DLQDetailClient initialTask={sample} />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.className).toContain('font-display');
    expect(h1.className).toContain('font-extrabold');
    // Italic-accent rule wired via descendant selector.
    expect(h1.className).toContain('[&_em]:font-serif');
    expect(h1.className).toContain('[&_em]:italic');
    expect(h1.className).toContain('[&_em]:text-emerald-deep');
  });

  it('action toolbar uses brand colour cues per outcome', () => {
    render(<DLQDetailClient initialTask={sample} />);
    const replay = screen.getByTestId('dlq-detail-replay');
    // Emerald CTA — positive (re-enqueue).
    expect(replay.className).toContain('bg-emerald');
    expect(replay.className).toContain('text-emerald-ink');

    const discard = screen.getByTestId('dlq-detail-discard');
    // Destructive variant — danger fill.
    expect(discard.className).toContain('bg-danger');

    const redact = screen.getByTestId('dlq-detail-redact');
    // Lavender accent — the masking path.
    expect(redact.className).toContain('lavender');
  });

  it('payload viewer renders mono on paper-3 (recessed code surface)', () => {
    render(<DLQDetailClient initialTask={sample} />);
    const payload = screen.getByTestId('dlq-detail-payload');
    expect(payload.className).toContain('font-mono');
    expect(payload.className).toContain('bg-paper-3');
  });

  it('error trace renders mono on paper-3', () => {
    render(<DLQDetailClient initialTask={sample} />);
    const trace = screen.getByTestId('dlq-detail-error-trace');
    expect(trace.className).toContain('font-mono');
    expect(trace.className).toContain('bg-paper-3');
  });

  it('queue badge picks up the lavender tone for the webhooks queue', () => {
    render(<DLQDetailClient initialTask={sample} />);
    // There are two queue badges on the page (header chip + metadata
    // dt/dd). Both should land on the lavender variant.
    const queueBadges = screen.getAllByText('webhooks');
    expect(queueBadges.length).toBeGreaterThan(0);
    expect(queueBadges[0]?.className).toContain('bg-lavender-soft');
  });
});
