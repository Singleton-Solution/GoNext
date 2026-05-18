'use client';

/**
 * StatusCard — one section of the System Status grid.
 *
 * Renders a title, a colored "traffic light" badge, an optional
 * one-line summary, and a list of key/value detail rows. The card is
 * pure-presentational: every page section builds the title/tone/rows
 * locally and hands them down. Layout + styling come from inline
 * styles so the card stays self-contained ahead of the design-system
 * extraction (#34).
 *
 * Tone colors:
 *
 *   ok      green  — section is healthy.
 *   warn    amber  — section is degraded but functioning (e.g. high
 *                    queue depth, in_use approaching max_conns).
 *   error   red    — section reported an Error or a hard fault.
 *   unknown grey   — source not configured; nothing to report.
 *
 * The badge text mirrors the tone in plain English so the page is
 * usable for operators who rely on screen readers or who have color-
 * vision differences.
 */
import type { CSSProperties, ReactElement, ReactNode } from 'react';
import type { StatusTone } from '../types';

export interface StatusCardRow {
  /** Label rendered in the left column. */
  label: string;
  /**
   * Value rendered in the right column. Allow ReactNode so callers
   * can drop in a `<code>` for a version string or a `<strong>` for
   * a counter without having to import this component's stylesheet.
   */
  value: ReactNode;
}

export interface StatusCardProps {
  title: string;
  tone: StatusTone;
  /** Short one-line summary shown beneath the title, before the rows. */
  summary?: string;
  rows?: StatusCardRow[];
  /**
   * When set, rendered as the card's error banner in place of the
   * default summary. The card's tone should typically be 'error' or
   * 'warn' when this is non-empty.
   */
  errorMessage?: string;
  /**
   * Optional data-testid pass-through so each card is selectable by
   * the page's tests without leaking the implementation detail into
   * the component itself.
   */
  testId?: string;
}

const TONE_LABEL: Record<StatusTone, string> = {
  ok: 'Healthy',
  warn: 'Degraded',
  error: 'Error',
  unknown: 'Unknown',
};

const TONE_COLORS: Record<StatusTone, { bg: string; fg: string; border: string }> = {
  ok: { bg: '#dcfce7', fg: '#166534', border: '#86efac' },
  warn: { bg: '#fef3c7', fg: '#92400e', border: '#fde68a' },
  error: { bg: '#fee2e2', fg: '#991b1b', border: '#fecaca' },
  unknown: { bg: '#f3f4f6', fg: '#4b5563', border: '#e5e7eb' },
};

const styles: Record<string, CSSProperties> = {
  card: {
    background: 'var(--color-surface, #ffffff)',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    padding: 16,
    display: 'flex',
    flexDirection: 'column',
    gap: 10,
    minHeight: 160,
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 12,
  },
  title: {
    margin: 0,
    fontSize: 14,
    fontWeight: 600,
    color: 'var(--color-text, #1c2024)',
  },
  summary: {
    color: 'var(--color-text-muted, #6b7280)',
    fontSize: 13,
    margin: 0,
  },
  rows: {
    listStyle: 'none',
    padding: 0,
    margin: 0,
    display: 'flex',
    flexDirection: 'column',
    gap: 4,
    fontSize: 13,
  },
  row: {
    display: 'flex',
    justifyContent: 'space-between',
    gap: 12,
    color: 'var(--color-text, #1c2024)',
  },
  rowLabel: {
    color: 'var(--color-text-muted, #6b7280)',
  },
  rowValue: {
    fontFeatureSettings: '"tnum"',
    fontVariantNumeric: 'tabular-nums',
  },
  errorBanner: {
    padding: '6px 8px',
    borderRadius: 4,
    background: '#fef2f2',
    color: '#991b1b',
    fontSize: 12,
    border: '1px solid #fecaca',
  },
};

function badgeStyle(tone: StatusTone): CSSProperties {
  const c = TONE_COLORS[tone];
  return {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '2px 8px',
    borderRadius: 999,
    background: c.bg,
    color: c.fg,
    border: `1px solid ${c.border}`,
    fontSize: 12,
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
  };
}

function dotStyle(tone: StatusTone): CSSProperties {
  const c = TONE_COLORS[tone];
  return {
    width: 8,
    height: 8,
    borderRadius: '50%',
    background: c.fg,
    display: 'inline-block',
  };
}

export function StatusCard({
  title,
  tone,
  summary,
  rows,
  errorMessage,
  testId,
}: StatusCardProps): ReactElement {
  return (
    <article
      style={styles.card}
      data-testid={testId}
      data-tone={tone}
      aria-label={title}
    >
      <div style={styles.header}>
        <h2 style={styles.title}>{title}</h2>
        <span
          style={badgeStyle(tone)}
          role="status"
          aria-label={`${title}: ${TONE_LABEL[tone]}`}
        >
          <span aria-hidden="true" style={dotStyle(tone)} />
          {TONE_LABEL[tone]}
        </span>
      </div>

      {errorMessage ? (
        <p style={styles.errorBanner} role="alert">
          {errorMessage}
        </p>
      ) : summary ? (
        <p style={styles.summary}>{summary}</p>
      ) : null}

      {rows && rows.length > 0 ? (
        <ul style={styles.rows}>
          {rows.map((row) => (
            <li key={row.label} style={styles.row}>
              <span style={styles.rowLabel}>{row.label}</span>
              <span style={styles.rowValue}>{row.value}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </article>
  );
}
