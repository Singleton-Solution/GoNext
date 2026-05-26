'use client';

/**
 * MigrationWizard — the client island that owns the wizard's state
 * machine and renders the active step.
 *
 * State machine:
 *
 *   source ─▶ options ─▶ preview ─▶ run ─▶ report
 *      ▲         ▲                            │
 *      └─────────┴──── (start over) ──────────┘
 *
 * The wizard runs entirely in component state — there's no URL
 * persistence (see types.ts for why). The API surface used here:
 *
 *   POST /api/v1/admin/migrate/dry-run   → DryRunResult
 *   POST /api/v1/admin/migrate/start     → { jobId }
 *   GET  /api/v1/admin/migrate/status?jobId=... → RunStatus
 *
 * We tolerate the API not being there yet — every endpoint has a
 * fallback "demo" path that returns synthetic data so the wizard can
 * be exercised on a bare admin deploy. The fallback is gated on a
 * single `useApi` flag that production deployments leave at `true`.
 *
 * Tests: MigrationWizard.test.tsx exercises each step transition and
 * the polling loop with a fake fetcher; a Playwright happy-path test
 * lives at tools/e2e/migrate-wizard.spec.ts.
 */

import {
  useCallback,
  useState,
  type ReactElement,
} from 'react';
import { Card } from '@/components/ui/card';
import { Check, ChevronRight } from 'lucide-react';
import { SourceStep } from './steps/SourceStep';
import { OptionsStep } from './steps/OptionsStep';
import { PreviewStep } from './steps/PreviewStep';
import { RunStep } from './steps/RunStep';
import { ReportStep } from './steps/ReportStep';
import {
  DEFAULT_OPTIONS,
  WIZARD_STEPS,
  type DryRunResult,
  type OptionsConfig,
  type RunStatus,
  type SourceConfig,
  type WizardStep,
} from './types';

/**
 * Step labels mirror the section headings the user sees. The list is
 * declared inline so it lives next to the component that renders it.
 */
const STEP_LABELS: Record<WizardStep, string> = {
  source: 'Source',
  options: 'Options',
  preview: 'Dry-run preview',
  run: 'Run',
  report: 'Report',
};

/**
 * Polling interval for the run-status endpoint. Two seconds is the
 * sweet spot between responsiveness and load — the importer reports
 * counts in batches of 100 posts, so much faster polling shows
 * identical numbers between rounds.
 */
export const POLL_INTERVAL_MS = 2000;

export interface MigrationWizardProps {
  /**
   * Optional fetcher override — tests inject a fake that resolves
   * synchronously without hitting the network.
   */
  fetcher?: typeof fetch;
}

export function MigrationWizard({
  fetcher = typeof fetch !== 'undefined' ? fetch : (undefined as unknown as typeof fetch),
}: MigrationWizardProps): ReactElement {
  const [step, setStep] = useState<WizardStep>('source');
  const [source, setSource] = useState<SourceConfig>({ kind: 'wxr-upload', wxrFile: null });
  const [options, setOptions] = useState<OptionsConfig>(DEFAULT_OPTIONS);
  const [preview, setPreview] = useState<DryRunResult | null>(null);
  const [runStatus, setRunStatus] = useState<RunStatus | null>(null);
  const [globalError, setGlobalError] = useState<string | null>(null);

  // Reset wraps the wizard back to step one with a clean state. Used
  // by the "Start over" button on the report step.
  const reset = useCallback(() => {
    setStep('source');
    setSource({ kind: 'wxr-upload', wxrFile: null });
    setOptions(DEFAULT_OPTIONS);
    setPreview(null);
    setRunStatus(null);
    setGlobalError(null);
  }, []);

  const advance = useCallback((to: WizardStep) => setStep(to), []);

  return (
    <div>
      <StepperHeader current={step} />
      {globalError && (
        <Card className="border-danger bg-danger-soft text-danger-ink p-4 mb-4">
          <p className="text-sm">{globalError}</p>
        </Card>
      )}
      <Card className="p-6">
        {step === 'source' && (
          <SourceStep
            value={source}
            onChange={setSource}
            onNext={() => advance('options')}
          />
        )}
        {step === 'options' && (
          <OptionsStep
            value={options}
            onChange={setOptions}
            onBack={() => advance('source')}
            onNext={() => advance('preview')}
          />
        )}
        {step === 'preview' && (
          <PreviewStep
            source={source}
            options={options}
            value={preview}
            onValue={setPreview}
            fetcher={fetcher}
            onBack={() => advance('options')}
            onNext={() => advance('run')}
            onError={setGlobalError}
          />
        )}
        {step === 'run' && (
          <RunStep
            source={source}
            options={options}
            value={runStatus}
            onValue={setRunStatus}
            fetcher={fetcher}
            onBack={() => advance('preview')}
            onNext={() => advance('report')}
            onError={setGlobalError}
          />
        )}
        {step === 'report' && (
          <ReportStep
            runStatus={runStatus}
            onRestart={reset}
          />
        )}
      </Card>
    </div>
  );
}

/**
 * StepperHeader is the breadcrumb-style progress strip across the
 * top of the wizard. Past steps are shown with a check, the active
 * step with a filled marker, future steps with a thin one. Click
 * targets are deliberately absent — the wizard validates between
 * steps, so jumping mid-flow would skip required fields.
 */
function StepperHeader({ current }: { current: WizardStep }): ReactElement {
  const currentIdx = WIZARD_STEPS.indexOf(current);
  return (
    <ol
      className="flex items-center gap-2 mb-6 overflow-x-auto"
      aria-label="Migration wizard progress"
    >
      {WIZARD_STEPS.map((s, idx) => {
        const isDone = idx < currentIdx;
        const isActive = idx === currentIdx;
        return (
          <li key={s} className="flex items-center gap-2 shrink-0">
            <span
              className={
                'inline-flex items-center justify-center w-6 h-6 rounded-full text-xs font-bold ' +
                (isDone
                  ? 'bg-emerald text-emerald-ink'
                  : isActive
                  ? 'bg-ink text-paper'
                  : 'bg-paper-3 text-fg-muted')
              }
              aria-current={isActive ? 'step' : undefined}
              data-testid={`step-marker-${s}`}
            >
              {isDone ? <Check width={14} height={14} aria-hidden /> : idx + 1}
            </span>
            <span
              className={
                'text-sm ' + (isActive ? 'font-bold text-ink' : 'text-fg-muted')
              }
            >
              {STEP_LABELS[s]}
            </span>
            {idx < WIZARD_STEPS.length - 1 && (
              <ChevronRight width={14} height={14} className="text-fg-subtle" aria-hidden />
            )}
          </li>
        );
      })}
    </ol>
  );
}
