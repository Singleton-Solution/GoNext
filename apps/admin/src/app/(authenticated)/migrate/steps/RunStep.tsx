'use client';

/**
 * RunStep — wizard step 4.
 *
 * Kicks off the actual import and polls the status endpoint until
 * the job terminates. Disables "Continue" until the job is done.
 *
 * API contract:
 *
 *   POST /api/v1/admin/migrate/start
 *   body: same as dry-run
 *   200: { jobId: string }
 *
 *   GET /api/v1/admin/migrate/status?jobId=<id>
 *   200: RunStatus
 *
 * As with the preview step, the API may not be wired up yet — in
 * which case the step simulates a successful run with a synthetic
 * progress curve so the wizard remains demoable.
 */

import {
  type ReactElement,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { apiBaseUrl } from '@/lib/api-client';
import { POLL_INTERVAL_MS } from '../MigrationWizard';
import { buildDryRunForm } from './PreviewStep';
import type { OptionsConfig, RunStatus, SourceConfig } from '../types';

export interface RunStepProps {
  source: SourceConfig;
  options: OptionsConfig;
  value: RunStatus | null;
  onValue: (next: RunStatus | null) => void;
  fetcher: typeof fetch;
  onBack: () => void;
  onNext: () => void;
  onError: (msg: string | null) => void;
}

export function RunStep({
  source,
  options,
  value,
  onValue,
  fetcher,
  onBack,
  onNext,
  onError,
}: RunStepProps): ReactElement {
  const [started, setStarted] = useState(value != null);
  const pollHandle = useRef<ReturnType<typeof setInterval> | null>(null);

  const stopPolling = useCallback(() => {
    if (pollHandle.current) {
      clearInterval(pollHandle.current);
      pollHandle.current = null;
    }
  }, []);

  // Synthetic mode advances a fake progress bar. Used as a fallback
  // when the API call to /start fails (server not running).
  const simulate = useCallback(() => {
    let pct = 0;
    const tick = () => {
      pct = Math.min(100, pct + 10);
      const counts = synthesizeCounts(source, pct);
      onValue({
        jobId: 'demo',
        status: pct >= 100 ? 'done' : 'running',
        percent: pct,
        phase: pct < 30 ? 'authors' : pct < 70 ? 'posts' : 'media',
        counts,
        errors: [],
      });
      if (pct >= 100) stopPolling();
    };
    tick(); // first frame
    pollHandle.current = setInterval(tick, 500);
  }, [source, onValue, stopPolling]);

  const start = useCallback(async () => {
    setStarted(true);
    onError(null);
    try {
      const form = buildDryRunForm(source, options);
      const res = await fetcher(`${apiBaseUrl}/api/v1/admin/migrate/start`, {
        method: 'POST',
        body: form,
        credentials: 'include',
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const { jobId } = (await res.json()) as { jobId: string };
      onValue({
        jobId,
        status: 'queued',
        percent: 0,
        phase: 'queued',
        counts: { authors: 0, categories: 0, tags: 0, posts: 0, attachments: 0, comments: 0, warnings: [] },
        errors: [],
      });
      // Begin polling.
      pollHandle.current = setInterval(async () => {
        try {
          const sres = await fetcher(
            `${apiBaseUrl}/api/v1/admin/migrate/status?jobId=${encodeURIComponent(jobId)}`,
            { credentials: 'include' },
          );
          if (!sres.ok) throw new Error(`HTTP ${sres.status}`);
          const status = (await sres.json()) as RunStatus;
          onValue(status);
          if (status.status === 'done' || status.status === 'failed') {
            stopPolling();
          }
        } catch (err) {
          onError(err instanceof Error ? err.message : String(err));
        }
      }, POLL_INTERVAL_MS);
    } catch (err) {
      onError(
        err instanceof Error
          ? `Run API unavailable (${err.message}); simulating.`
          : 'Run API unavailable; simulating.',
      );
      simulate();
    }
  }, [source, options, fetcher, onValue, onError, simulate, stopPolling]);

  // Auto-start on first mount.
  useEffect(() => {
    if (!started) {
      void start();
    }
    return stopPolling;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const status = value;
  const done = status?.status === 'done';

  return (
    <section aria-label="Run">
      <h2 className="text-lg font-bold mb-1">4. Run</h2>
      <p className="text-fg-muted text-sm mb-6">
        Importing now. This is the only step that writes to the database.
      </p>

      <div className="mb-4">
        <div className="flex items-center justify-between text-sm mb-1">
          <span data-testid="run-phase">
            {status?.phase ?? 'Starting…'}
          </span>
          <span data-testid="run-percent">{status?.percent ?? 0}%</span>
        </div>
        <div
          className="w-full h-3 bg-paper-3 rounded-full overflow-hidden"
          role="progressbar"
          aria-valuenow={status?.percent ?? 0}
          aria-valuemin={0}
          aria-valuemax={100}
          data-testid="run-progressbar"
        >
          <div
            className="h-full bg-emerald transition-all duration-300"
            style={{ width: `${Math.min(100, status?.percent ?? 0)}%` }}
          />
        </div>
      </div>

      {status && (
        <Card className="p-4 mb-4 text-sm" data-testid="run-counts">
          <dl className="grid grid-cols-2 sm:grid-cols-3 gap-2">
            <dt className="text-fg-muted">Authors</dt>
            <dd className="font-bold col-span-2 sm:col-span-2">{status.counts.authors}</dd>
            <dt className="text-fg-muted">Posts</dt>
            <dd className="font-bold col-span-2 sm:col-span-2">{status.counts.posts}</dd>
            <dt className="text-fg-muted">Attachments</dt>
            <dd className="font-bold col-span-2 sm:col-span-2">{status.counts.attachments}</dd>
            <dt className="text-fg-muted">Comments</dt>
            <dd className="font-bold col-span-2 sm:col-span-2">{status.counts.comments}</dd>
          </dl>
        </Card>
      )}

      {status?.errors && status.errors.length > 0 && (
        <Card className="border-danger bg-danger-soft p-4 mb-4">
          <h3 className="font-bold text-sm mb-2">Errors</h3>
          <ul className="text-xs space-y-1">
            {status.errors.slice(0, 25).map((e, i) => (
              <li key={i}>{e}</li>
            ))}
          </ul>
        </Card>
      )}

      <div className="flex justify-between">
        <Button onClick={onBack} variant="ghost" disabled={!done} data-testid="run-back">
          Back
        </Button>
        <Button
          onClick={onNext}
          variant="primary"
          disabled={!done}
          data-testid="run-next"
        >
          See report
        </Button>
      </div>
    </section>
  );
}

/**
 * synthesizeCounts produces a believable count vector for the
 * synthetic progress curve. We anchor the totals to the source kind
 * so a WXR upload's mocked counts feel like a small-blog import.
 */
function synthesizeCounts(
  source: SourceConfig,
  pct: number,
): RunStatus['counts'] {
  const totals = source.kind === 'wxr-upload'
    ? { authors: 5, posts: 30, attachments: 12, comments: 50, categories: 4, tags: 18 }
    : source.kind === 'rest-url'
    ? { authors: 3, posts: 25, attachments: 18, comments: 40, categories: 5, tags: 12 }
    : { authors: 0, posts: 0, attachments: 0, comments: 0, categories: 0, tags: 0 };
  const ratio = pct / 100;
  return {
    authors: Math.round(totals.authors * ratio),
    categories: Math.round(totals.categories * ratio),
    tags: Math.round(totals.tags * ratio),
    posts: Math.round(totals.posts * ratio),
    attachments: Math.round(totals.attachments * ratio),
    comments: Math.round(totals.comments * ratio),
    warnings: [],
  };
}
