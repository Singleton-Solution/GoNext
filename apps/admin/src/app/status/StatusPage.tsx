'use client';

/**
 * StatusPage — the operator-facing System Status surface.
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
 * Data flow: useStatusReport (below) wraps a single fetch+state pair.
 * Refreshing replaces the report; an in-flight fetch is aborted when a
 * newer one starts so unmount or a click during a slow request doesn't
 * leak a setState into a torn-down tree.
 *
 * Error handling: a failed fetch keeps the last good report on screen
 * and renders an error banner above the grid — the operator still sees
 * what they saw 30 seconds ago, plus a clear "something went wrong"
 * signal. We don't show a full-page skeleton on every refresh because
 * the page is too low-frequency for that to be acceptable.
 */
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactElement,
} from 'react';
import { ApiError } from '../api-client';
import { fetchStatusReport } from './api';
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
import { StatusCard } from './components/StatusCard';
import type { StatusReport } from './types';

const REFRESH_INTERVAL_MS = 30_000;

const styles: Record<string, CSSProperties> = {
  header: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 12,
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 16,
  },
  toolbar: {
    display: 'flex',
    gap: 8,
    alignItems: 'center',
  },
  meta: {
    color: 'var(--color-text-muted, #6b7280)',
    fontSize: 13,
  },
  grid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
    gap: 12,
  },
  sectionHeading: {
    fontSize: 14,
    fontWeight: 600,
    margin: '24px 0 8px',
    color: 'var(--color-text-muted, #6b7280)',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  },
  errorBanner: {
    padding: '10px 12px',
    marginBottom: 12,
    border: '1px solid #fecaca',
    background: '#fef2f2',
    color: '#991b1b',
    borderRadius: 6,
    fontSize: 13,
  },
  infoBanner: {
    padding: '10px 12px',
    marginBottom: 12,
    border: '1px solid var(--color-border, #e4e6ea)',
    background: 'var(--color-surface, #ffffff)',
    color: 'var(--color-text-muted, #6b7280)',
    borderRadius: 6,
    fontSize: 13,
  },
  button: {
    padding: '6px 12px',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    background: 'var(--color-surface, #ffffff)',
    color: 'var(--color-text, #1c2024)',
    fontSize: 13,
  },
  buttonPrimary: {
    padding: '6px 12px',
    border: '1px solid var(--color-accent, #2563eb)',
    borderRadius: 6,
    background: 'var(--color-accent, #2563eb)',
    color: '#ffffff',
    fontSize: 13,
    fontWeight: 500,
  },
};

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
      const message = err instanceof ApiError
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

export function StatusPage(): ReactElement {
  const { report, loading, error, refreshedAt, refresh } = useStatusReport();
  const [copyStatus, setCopyStatus] = useState<'idle' | 'copied' | 'failed'>('idle');

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

  return (
    <section data-testid="status-page">
      <div style={styles.header}>
        <div>
          <h1>System Status</h1>
          <p style={styles.meta}>
            {report?.generated
              ? `Last refreshed: ${new Date(report.generated).toLocaleString()}`
              : refreshedAt
              ? `Last refreshed: ${new Date(refreshedAt).toLocaleString()}`
              : 'Loading…'}
          </p>
        </div>
        <div style={styles.toolbar} role="toolbar" aria-label="Status actions">
          <button
            type="button"
            style={styles.button}
            onClick={() => {
              void refresh();
            }}
            disabled={loading}
            aria-label="Refresh status"
          >
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
          <button
            type="button"
            style={styles.buttonPrimary}
            onClick={() => {
              void onCopy();
            }}
            disabled={!report}
            aria-label="Copy diagnostic to clipboard"
          >
            {copyStatus === 'copied'
              ? 'Copied!'
              : copyStatus === 'failed'
              ? 'Copy failed'
              : 'Copy diagnostic'}
          </button>
        </div>
      </div>

      {error ? (
        <div role="alert" style={styles.errorBanner} data-testid="status-error">
          Couldn&apos;t refresh status: {error}. Showing the last successful
          result.
        </div>
      ) : null}

      {!report && loading ? (
        <div style={styles.infoBanner} data-testid="status-loading">
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

function StatusGrid({ report }: StatusGridProps): ReactElement {
  const bi = deriveBuildInfo(report);
  const db = deriveDatabase(report.database);
  const rds = deriveRedis(report.redis);
  const mig = deriveMigrations(report.migrations);
  const theme = deriveTheme(report.theme);
  const plugins = derivePlugins(report.plugins);
  const disk = deriveDisk(report.disk);

  return (
    <div data-testid="status-grid">
      <h2 style={styles.sectionHeading}>Build</h2>
      <div style={styles.grid}>
        <StatusCard
          title="Build info"
          tone={bi.tone}
          rows={bi.rows}
          testId="card-build"
        />
      </div>

      <h2 style={styles.sectionHeading}>Infrastructure</h2>
      <div style={styles.grid}>
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

      <h2 style={styles.sectionHeading}>Application</h2>
      <div style={styles.grid}>
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

      <h2 style={styles.sectionHeading}>Queues</h2>
      <div style={styles.grid}>
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
    </div>
  );
}
