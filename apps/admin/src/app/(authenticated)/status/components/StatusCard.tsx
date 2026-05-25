'use client';

/**
 * StatusCard — one section of the System Status grid, restyled
 * against the Living-Systems brand.
 *
 * Visual treatment follows the data-viz language from
 * `docs/design/ui_kits/admin/pulse.html`: the card surface is a
 * tone-tinted paper-2 panel, the heading reads as a small-caps
 * eyebrow, the value/value rows are tabular monospace, and the
 * status badge uses a soft brand-token tint instead of the
 * pre-brand traffic-light hexes.
 *
 * Tone → tint mapping (canonical, mirrored from the handoff):
 *
 *   ok      → emerald-soft surface · emerald-deep text  (healthy)
 *   warn    → lavender-soft surface · lavender-deep text (degraded)
 *   error   → danger-soft surface · danger text          (critical)
 *   unknown → paper-3 surface · fg-subtle text           (skipped)
 *
 * Lucide icons (`CheckCircle2 / AlertTriangle / XCircle / MinusCircle`)
 * stand in for the textual badge so a glance gives operators the
 * same state read as the verbose label. The badge text mirrors the
 * tone in plain English for screen readers and color-vision
 * differences.
 */
import {
  AlertTriangle,
  CheckCircle2,
  MinusCircle,
  XCircle,
  type LucideIcon,
} from 'lucide-react';
import type { ReactElement, ReactNode } from 'react';

import { cn } from '@/lib/utils';
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
  error: 'Critical',
  unknown: 'Skipped',
};

const TONE_ICON: Record<StatusTone, LucideIcon> = {
  ok: CheckCircle2,
  warn: AlertTriangle,
  error: XCircle,
  unknown: MinusCircle,
};

/**
 * Card surface tint per tone. Maps to the four brand-soft surfaces
 * declared in `tokens.css`. The hairline left-edge keeps the badge
 * legible without leaning on a hard border colour.
 */
const TONE_CARD: Record<StatusTone, string> = {
  ok: 'bg-emerald-soft/40 border-emerald/30',
  warn: 'bg-lavender-soft/60 border-lavender/30',
  error: 'bg-danger-soft border-danger/30',
  unknown: 'bg-paper-3 border-border',
};

const TONE_BADGE: Record<StatusTone, string> = {
  ok: 'bg-emerald-soft text-emerald-deep',
  warn: 'bg-lavender-soft text-lavender-deep',
  error: 'bg-danger-soft text-danger',
  unknown: 'bg-paper-3 text-fg-subtle',
};

const TONE_ICON_TINT: Record<StatusTone, string> = {
  ok: 'text-emerald-deep',
  warn: 'text-lavender-deep',
  error: 'text-danger',
  unknown: 'text-fg-subtle',
};

export function StatusCard({
  title,
  tone,
  summary,
  rows,
  errorMessage,
  testId,
}: StatusCardProps): ReactElement {
  const Icon = TONE_ICON[tone];
  return (
    <article
      data-testid={testId}
      data-tone={tone}
      aria-label={title}
      className={cn(
        'flex min-h-[160px] flex-col gap-3 rounded-lg border p-5 shadow-xs',
        'transition-shadow duration-[160ms] ease-brand hover:shadow-md',
        TONE_CARD[tone],
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2">
          <Icon
            aria-hidden="true"
            className={cn('h-4 w-4 flex-shrink-0', TONE_ICON_TINT[tone])}
          />
          <h2 className="m-0 font-sans text-sm font-semibold leading-tight text-ink">
            {title}
          </h2>
        </div>
        <span
          role="status"
          aria-label={`${title}: ${TONE_LABEL[tone]}`}
          className={cn(
            'inline-flex items-center gap-[6px] rounded-pill px-2 py-[2px] font-mono text-[10px] font-semibold uppercase tracking-wide',
            TONE_BADGE[tone],
          )}
        >
          <span
            aria-hidden="true"
            className={cn(
              'h-[6px] w-[6px] rounded-pill bg-current',
              tone === 'ok' && 'animate-pulse',
            )}
          />
          {TONE_LABEL[tone]}
        </span>
      </div>

      {errorMessage ? (
        <p
          role="alert"
          className="rounded-sm border border-danger/30 bg-danger-soft/70 px-2 py-1 font-mono text-xs text-danger"
        >
          {errorMessage}
        </p>
      ) : summary ? (
        <p className="m-0 font-sans text-sm leading-normal text-fg-muted">
          {summary}
        </p>
      ) : null}

      {rows && rows.length > 0 ? (
        <ul className="m-0 flex list-none flex-col gap-1 p-0 font-sans text-sm">
          {rows.map((row) => (
            <li
              key={row.label}
              className="flex justify-between gap-3 text-ink"
            >
              <span className="text-fg-subtle">{row.label}</span>
              <span className="font-mono tabular-nums text-ink">
                {row.value}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </article>
  );
}
