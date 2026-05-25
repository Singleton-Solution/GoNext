'use client';

/**
 * StatusPage — the operator-facing System Status surface, restyled
 * against the Living-Systems brand.
 *
 * The page is a client component because three of its responsibilities
 * are inherently interactive:
 *
 *   1. Auto-refresh every 30 seconds (matches the operator-visible
 *      blast radius of a sick subsystem — a Redis blip should clear
 *      itself well inside one tick).
 *   2. Manual "Refresh" button so an operator chasing a regression
 *      can fetch without waiting for the next tick.
 *   3. "Copy diagnostic" button that writes the redacted JSON to the
 *      clipboard via navigator.clipboard.writeText, for pasting into
 *      a support ticket.
 *
 * Visual treatment: Archivo display headline with the italic-accent
 * rule (`System <em>status</em>.`), a soft "Refreshed Ns ago"
 * auto-refresh badge in monospace + italic accent, and an emerald
 * "Copy diagnostic" Button primitive with a Lucide Copy icon. The
 * tone-tinted card surfaces live inside StatusCard; the page itself
 * stays cream and lets the cards drive the colour.
 *
 * Error handling: a failed fetch keeps the last good report on screen
 * and renders an error banner above the grid — the operator still sees
 * what they saw 30 seconds ago, plus a clear "something went wrong"
 * signal. We don't show a full-page skeleton on every refresh because
 * the page is too low-frequency for that to be acceptable.
 */
import { Check, Copy, RefreshCw, type LucideIcon } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactElement,
} from 'react';

import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { ApiError } from '@/lib/api-client';
import { cn } from '@/lib/utils';

import { fetchStatusReport } from './api';
import { StatusCard } from './components/StatusCard';
import {
  deriveBuildInfo,
  deriveDatabase,
  deriveDisk,
  deriveMigrations,
  derivePlugins,
  deriveQueue,
  deriveRedis,
  deriveTheme,
  redactDiagnostic,
} from './derive';
import type { StatusReport } from './types';

const REFRESH_INTERVAL_MS = 30_000;

interface UseStatusReportState {
  report: StatusReport | null;
  loading: boolean;
  error: string | null;
  refreshedAt: number | null;
}

function useStatusReport(): UseStatusReportState & {
  refresh: () => Promise<void>;
} {
  const [state, setState] = useState<UseStatusReportState>({
    report: null,
    loading: true,
    error: null,
    refreshedAt: null,
  });

  // Hold the in-flight AbortController so a new refresh tick cancels
  // the previous one. Strict-mode double-mount is also handled here:
  // the cleanup aborts the controller, preventing a torn-down setState.
  const abortRef = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    setState((s) => ({ ...s, loading: true, error: null }));
    try {
      const report = await fetchStatusReport(controller.signal);
      if (controller.signal.aborted) return;
      setState({
        report,
        loading: false,
        error: null,
        refreshedAt: Date.now(),
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      const message =
        err instanceof ApiError
          ? `API error ${err.status}: ${err.statusText}`
          : err instanceof Error
            ? err.message
            : 'Unknown error';
      setState((s) => ({ ...s, loading: false, error: message }));
    }
  }, []);

  useEffect(() => {
    void refresh();
    const id = setInterval(() => {
      void refresh();
    }, REFRESH_INTERVAL_MS);
    return () => {
      clearInterval(id);
      abortRef.current?.abort();
    };
  }, [refresh]);

  return { ...state, refresh };
}

/**
 * formatAgo turns a "last refreshed at" timestamp into the
 * "Refreshed *Ns* ago" auto-refresh chip text. Live-tickers like
 * this should always emit a label, even on first paint — fallback
 * to "just now" for any value <1s.
 */
function formatAgo(refreshedAt: number, now: number): string {
  const delta = Math.max(0, Math.floor((now - refreshedAt) / 1000));
  if (delta < 1) return 'just now';
  if (delta < 60) return `${delta}s ago`;
  const minutes = Math.floor(delta / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
}

/**
 * AutoRefreshBadge — small mono chip showing the "Refreshed Ns ago"
 * label. Ticks once a second by holding its own `now` clock so the
 * label visibly counts up between fetches.
 */
function AutoRefreshBadge({
  refreshedAt,
}: {
  refreshedAt: number | null;
}): ReactElement {
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return (
    <span
      className={cn(
        'inline-flex items-center gap-[6px] rounded-pill border border-emerald/30 bg-emerald-soft/50 px-3 py-1',
        'font-mono text-xs text-emerald-deep',
      )}
      aria-live="polite"
      data-testid="status-refreshed-ago"
    >
      <span
        aria-hidden="true"
        className="h-[6px] w-[6px] rounded-pill bg-emerald-deep"
        // The dot pulses while data is being treated as "live"; for
        // SSR'd tests we keep it static but the animation is fine to
        // request on the client.
        style={{ animation: 'pulse 2s ease-in-out infinite' }}
      />
      Refreshed{' '}
      <em className="font-serif italic text-emerald-deep">
        {refreshedAt ? formatAgo(refreshedAt, now) : 'never'}
      </em>
    </span>
  );
}

export function StatusPage(): ReactElement {
  const { report, loading, error, refreshedAt, refresh } = useStatusReport();
  const [copyStatus, setCopyStatus] = useState<'idle' | 'copied' | 'failed'>(
    'idle',
  );

  const onCopy = useCallback(async () => {
    if (!report) return;
    const text = redactDiagnostic(report);
    try {
      if (
        typeof navigator !== 'undefined' &&
        navigator.clipboard &&
        typeof navigator.clipboard.writeText === 'function'
      ) {
        await navigator.clipboard.writeText(text);
        setCopyStatus('copied');
      } else {
        // Clipboard API unavailable (insecure context, JSDOM). Fall
        // back to a hidden textarea + execCommand so the page is still
        // usable in those environments.
        const ta = document.createElement('textarea');
        ta.value = text;
        document.body.appendChild(ta);
        ta.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(ta);
        setCopyStatus(ok ? 'copied' : 'failed');
      }
    } catch {
      setCopyStatus('failed');
    }
    window.setTimeout(() => setCopyStatus('idle'), 1500);
  }, [report]);

  const CopyIcon: LucideIcon = copyStatus === 'copied' ? Check : Copy;

  return (
    <section data-testid="status-page" className="flex flex-col gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4 border-b border-border pb-4">
        <div className="flex flex-col gap-2">
          <span className="font-sans text-xs font-medium uppercase tracking-wide text-emerald-deep">
            Observability
          </span>
          <Headline as="h1" size="sub">
            System <em>status</em>.
          </Headline>
          <p className="m-0 max-w-[540px] font-sans text-sm text-fg-muted">
            A live readout of the runtime — database, queues, themes,
            plugins. Every <em className="font-serif italic text-emerald-deep">30s</em> a
            fresh report ticks in.
          </p>
        </div>
        <div
          className="flex flex-wrap items-center gap-2"
          role="toolbar"
          aria-label="Status actions"
        >
          <AutoRefreshBadge refreshedAt={refreshedAt} />
          <Button
            type="button"
            variant="default"
            size="sm"
            onClick={() => {
              void refresh();
            }}
            disabled={loading}
            aria-label="Refresh status"
          >
            <RefreshCw
              aria-hidden="true"
              className={cn('h-4 w-4', loading && 'animate-spin')}
            />
            {loading ? 'Refreshing…' : 'Refresh'}
          </Button>
          <Button
            type="button"
            variant="emerald"
            size="sm"
            onClick={() => {
              void onCopy();
            }}
            disabled={!report}
            aria-label="Copy diagnostic to clipboard"
          >
            <CopyIcon aria-hidden="true" className="h-4 w-4" />
            {copyStatus === 'copied'
              ? 'Copied!'
              : copyStatus === 'failed'
                ? 'Copy failed'
                : 'Copy diagnostic'}
          </Button>
        </div>
      </div>

      {error ? (
        <div
          role="alert"
          data-testid="status-error"
          className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 font-sans text-sm text-danger"
        >
          Couldn&apos;t refresh status: {error}. Showing the last successful
          result.
        </div>
      ) : null}

      {!report && loading ? (
        <div
          data-testid="status-loading"
          className="rounded-md border border-border bg-paper-2 px-3 py-2 font-sans text-sm text-fg-muted"
        >
          Loading system status…
        </div>
      ) : null}

      {report ? <StatusGrid report={report} /> : null}
    </section>
  );
}

interface StatusGridProps {
  report: StatusReport;
}

function SectionHeading({ children }: { children: ReactElement | string }): ReactElement {
  return (
    <h2 className="m-0 mb-2 mt-2 font-sans text-xs font-semibold uppercase tracking-wide text-fg-subtle">
      {children}
    </h2>
  );
}

function StatusGrid({ report }: StatusGridProps): ReactElement {
  const bi = deriveBuildInfo(report);
  const db = deriveDatabase(report.database);
  const rds = deriveRedis(report.redis);
  const mig = deriveMigrations(report.migrations);
  const theme = deriveTheme(report.theme);
  const plugins = derivePlugins(report.plugins);
  const disk = deriveDisk(report.disk);

  const gridClass =
    'grid gap-3 grid-cols-[repeat(auto-fit,minmax(260px,1fr))]';

  return (
    <div data-testid="status-grid" className="flex flex-col gap-5">
      <section className="flex flex-col gap-2">
        <SectionHeading>Build</SectionHeading>
        <div className={gridClass}>
          <StatusCard
            title="Build info"
            tone={bi.tone}
            rows={bi.rows}
            testId="card-build"
          />
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <SectionHeading>Infrastructure</SectionHeading>
        <div className={gridClass}>
          <StatusCard
            title="Database"
            tone={db.tone}
            summary={db.summary}
            errorMessage={db.errorMessage}
            rows={db.rows}
            testId="card-database"
          />
          <StatusCard
            title="Redis"
            tone={rds.tone}
            summary={rds.summary}
            errorMessage={rds.errorMessage}
            rows={rds.rows}
            testId="card-redis"
          />
          <StatusCard
            title="Migrations"
            tone={mig.tone}
            summary={mig.summary}
            errorMessage={mig.errorMessage}
            rows={mig.rows}
            testId="card-migrations"
          />
          <StatusCard
            title="Disk"
            tone={disk.tone}
            summary={disk.summary}
            errorMessage={disk.errorMessage}
            rows={disk.rows}
            testId="card-disk"
          />
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <SectionHeading>Application</SectionHeading>
        <div className={gridClass}>
          <StatusCard
            title="Theme"
            tone={theme.tone}
            summary={theme.summary}
            errorMessage={theme.errorMessage}
            rows={theme.rows}
            testId="card-theme"
          />
          <StatusCard
            title="Plugins"
            tone={plugins.tone}
            summary={plugins.summary}
            errorMessage={plugins.errorMessage}
            rows={plugins.rows}
            testId="card-plugins"
          />
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <SectionHeading>Queues</SectionHeading>
        <div className={gridClass}>
          {report.queues.length === 0 ? (
            <StatusCard
              title="Queues"
              tone="unknown"
              summary="No queue inspector configured."
              testId="card-queues-empty"
            />
          ) : (
            report.queues.map((q) => {
              const data = deriveQueue(q);
              return (
                <StatusCard
                  key={q.name}
                  title={`Queue: ${q.name}`}
                  tone={data.tone}
                  summary={data.summary}
                  errorMessage={data.errorMessage}
                  rows={data.rows}
                  testId={`card-queue-${q.name}`}
                />
              );
            })
          )}
        </div>
      </section>
    </div>
  );
}
