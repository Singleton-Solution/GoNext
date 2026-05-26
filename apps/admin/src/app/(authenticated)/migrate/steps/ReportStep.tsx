'use client';

/**
 * ReportStep — wizard step 5 (terminal).
 *
 * Renders the final outcome counts plus pointers to the plugin
 * replacement guide for surfaces that didn't migrate. The
 * "Start over" button rewinds the wizard if the operator wants
 * to run another source.
 *
 * No new network calls happen here — the data comes from the run
 * status passed in by the wizard's state machine.
 */

import { type ReactElement } from 'react';
import Link from 'next/link';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { CheckCircle2, AlertTriangle, ExternalLink } from 'lucide-react';
import type { RunStatus } from '../types';

export interface ReportStepProps {
  runStatus: RunStatus | null;
  onRestart: () => void;
}

export function ReportStep({ runStatus, onRestart }: ReportStepProps): ReactElement {
  if (!runStatus) {
    return (
      <section aria-label="Report" data-testid="report-empty">
        <h2 className="text-lg font-bold mb-1">5. Report</h2>
        <p className="text-fg-muted text-sm">No run data. Start over from step 1.</p>
        <div className="mt-4">
          <Button onClick={onRestart} variant="primary" data-testid="report-restart">
            Start over
          </Button>
        </div>
      </section>
    );
  }

  const succeeded = runStatus.status === 'done' && runStatus.errors.length === 0;
  const partial = runStatus.status === 'done' && runStatus.errors.length > 0;
  const failed = runStatus.status === 'failed';

  return (
    <section aria-label="Report">
      <h2 className="text-lg font-bold mb-1">5. Report</h2>
      <p className="text-fg-muted text-sm mb-6">
        {succeeded && 'All records imported cleanly.'}
        {partial && 'Import finished with some per-record errors. Review below.'}
        {failed && 'The import did not complete.'}
      </p>

      <Card
        className={
          'p-4 mb-6 flex items-start gap-3 ' +
          (succeeded
            ? 'border-emerald bg-emerald-soft'
            : partial
            ? 'border-warn bg-warn-soft'
            : 'border-danger bg-danger-soft')
        }
        data-testid="report-banner"
      >
        {succeeded ? (
          <CheckCircle2 className="text-emerald-deep shrink-0" aria-hidden />
        ) : (
          <AlertTriangle className="shrink-0" aria-hidden />
        )}
        <div className="text-sm">
          <strong>Job {runStatus.jobId}</strong> — status: {runStatus.status}
        </div>
      </Card>

      <h3 className="font-bold text-sm mb-2">Final counts</h3>
      <dl
        className="grid grid-cols-2 sm:grid-cols-3 gap-3 mb-6"
        data-testid="report-counts"
      >
        <ReportTile label="Authors" value={runStatus.counts.authors} />
        <ReportTile label="Posts" value={runStatus.counts.posts} />
        <ReportTile label="Attachments" value={runStatus.counts.attachments} />
        <ReportTile label="Categories" value={runStatus.counts.categories} />
        <ReportTile label="Tags" value={runStatus.counts.tags} />
        <ReportTile label="Comments" value={runStatus.counts.comments} />
      </dl>

      {runStatus.errors.length > 0 && (
        <Card className="p-4 mb-6" data-testid="report-errors">
          <h3 className="font-bold text-sm mb-2">
            Per-record errors ({runStatus.errors.length})
          </h3>
          <ul className="text-xs space-y-1 max-h-48 overflow-y-auto">
            {runStatus.errors.slice(0, 50).map((e, i) => (
              <li key={i}>{e}</li>
            ))}
            {runStatus.errors.length > 50 && (
              <li className="text-fg-muted">
                … and {runStatus.errors.length - 50} more.
              </li>
            )}
          </ul>
        </Card>
      )}

      <Card className="p-4 mb-6">
        <h3 className="font-bold text-sm mb-2">Next steps</h3>
        <ul className="text-sm space-y-2">
          <li className="flex items-start gap-2">
            <ExternalLink width={14} className="mt-1 shrink-0" aria-hidden />
            <span>
              Run{' '}
              <code className="bg-paper-3 rounded px-1">
                gonext migrate replacements
              </code>{' '}
              to generate a plugin-by-plugin migration guide for surfaces
              that don&apos;t yet have a GoNext equivalent.
            </span>
          </li>
          <li className="flex items-start gap-2">
            <ExternalLink width={14} className="mt-1 shrink-0" aria-hidden />
            <Link href="/posts" className="text-emerald-deep underline">
              Open the Posts list
            </Link>
            <span>— spot-check the imported content.</span>
          </li>
          <li className="flex items-start gap-2">
            <ExternalLink width={14} className="mt-1 shrink-0" aria-hidden />
            <Link href="/redirects" className="text-emerald-deep underline">
              Configure redirects
            </Link>
            <span>— preserve any deep-linked URLs from the old site.</span>
          </li>
        </ul>
      </Card>

      <div className="flex justify-end">
        <Button onClick={onRestart} variant="primary" data-testid="report-restart">
          Start over
        </Button>
      </div>
    </section>
  );
}

function ReportTile({ label, value }: { label: string; value: number }): ReactElement {
  return (
    <div className="bg-paper-2 rounded-md p-3">
      <div className="text-fg-muted text-xs uppercase tracking-wide">{label}</div>
      <div className="text-2xl font-bold mt-1">{value.toLocaleString()}</div>
    </div>
  );
}
