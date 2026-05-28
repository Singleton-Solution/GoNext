'use client';

/**
 * PreviewStep — wizard step 3.
 *
 * Submits the source + options to the API's dry-run endpoint and
 * shows the resulting counts before the operator commits to a write.
 *
 * API contract (mock-friendly):
 *
 *   POST /api/v1/admin/migrate/dry-run
 *   body: multipart/form-data
 *     file?      → File (when source.kind === 'wxr-upload')
 *     restUrl?   → string
 *     acfPath?   → string
 *     options    → JSON of OptionsConfig
 *   200: DryRunResult
 *   4xx/5xx: { error: string }
 *
 * If the call fails OR if the API isn't wired up (network error
 * caught), the step displays a "try again" button and a synthetic
 * preview so the wizard remains demoable on a bare deploy.
 */

import {
  type ReactElement,
  useCallback,
  useEffect,
  useState,
} from 'react';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { apiBaseUrl } from '@/lib/api-client';
import type {
  DryRunResult,
  OptionsConfig,
  SourceConfig,
} from '../types';

export interface PreviewStepProps {
  source: SourceConfig;
  options: OptionsConfig;
  value: DryRunResult | null;
  onValue: (next: DryRunResult | null) => void;
  fetcher: typeof fetch;
  onBack: () => void;
  onNext: () => void;
  onError: (msg: string | null) => void;
}

export function PreviewStep({
  source,
  options,
  value,
  onValue,
  fetcher,
  onBack,
  onNext,
  onError,
}: PreviewStepProps): ReactElement {
  const [loading, setLoading] = useState(false);

  const fetchPreview = useCallback(async () => {
    setLoading(true);
    onError(null);
    try {
      const form = buildDryRunForm(source, options);
      const res = await fetcher(`${apiBaseUrl()}/api/v1/admin/migrate/dry-run`, {
        method: 'POST',
        body: form,
        credentials: 'include',
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      const json = (await res.json()) as DryRunResult;
      onValue(json);
    } catch (err) {
      // The API may not be wired up yet — surface a synthetic preview
      // so the wizard is still demoable. Production deploys with a
      // real API path will surface the real numbers here.
      onValue(syntheticPreview(source));
      onError(
        err instanceof Error
          ? `Dry-run API unavailable (${err.message}); showing demo data.`
          : 'Dry-run API unavailable; showing demo data.',
      );
    } finally {
      setLoading(false);
    }
  }, [source, options, fetcher, onValue, onError]);

  // Auto-fetch on mount so the operator sees numbers immediately.
  useEffect(() => {
    if (value == null && !loading) {
      void fetchPreview();
    }
    // We intentionally fire once on mount; refetch is operator-driven.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <section aria-label="Dry-run preview">
      <h2 className="text-lg font-bold mb-1">3. Dry-run preview</h2>
      <p className="text-fg-muted text-sm mb-6">
        The importer walks the source without writing. Confirm the counts
        match what you expect.
      </p>

      {loading && (
        <p className="text-sm text-fg-muted" data-testid="preview-loading">
          Counting records…
        </p>
      )}

      {value && (
        <div data-testid="preview-counts" className="grid grid-cols-2 sm:grid-cols-3 gap-3 mb-4">
          <CountTile label="Authors" value={value.authors} />
          <CountTile label="Posts" value={value.posts} />
          <CountTile label="Pages + others" value={Math.max(0, value.posts - value.attachments)} />
          <CountTile label="Categories" value={value.categories} />
          <CountTile label="Tags" value={value.tags} />
          <CountTile label="Attachments" value={value.attachments} />
          <CountTile label="Comments" value={value.comments} />
        </div>
      )}

      {value && value.warnings.length > 0 && (
        <Card className="border-warn bg-warn-soft p-4 mb-4" data-testid="preview-warnings">
          <h3 className="font-bold text-sm mb-2">Warnings</h3>
          <ul className="text-xs space-y-1">
            {value.warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </Card>
      )}

      <div className="flex justify-between">
        <Button onClick={onBack} variant="ghost" data-testid="preview-back">
          Back
        </Button>
        <div className="flex gap-2">
          <Button
            onClick={() => {
              onValue(null);
              void fetchPreview();
            }}
            variant="outline"
            disabled={loading}
            data-testid="preview-refresh"
          >
            Re-run preview
          </Button>
          <Button
            onClick={onNext}
            variant="primary"
            disabled={loading || value == null}
            data-testid="preview-next"
          >
            Start migration
          </Button>
        </div>
      </div>
    </section>
  );
}

function CountTile({ label, value }: { label: string; value: number }): ReactElement {
  return (
    <div className="bg-paper-2 rounded-md p-3" data-testid={`count-tile-${label}`}>
      <div className="text-fg-muted text-xs uppercase tracking-wide">{label}</div>
      <div className="text-2xl font-bold mt-1">{value.toLocaleString()}</div>
    </div>
  );
}

/**
 * buildDryRunForm assembles the multipart body for the dry-run call.
 * The options JSON travels as a single field so the API doesn't have
 * to parse a forest of flat keys.
 */
export function buildDryRunForm(source: SourceConfig, options: OptionsConfig): FormData {
  const form = new FormData();
  form.append('kind', source.kind);
  if (source.kind === 'wxr-upload' && source.wxrFile) {
    form.append('file', source.wxrFile);
  }
  if (source.kind === 'rest-url' && source.restUrl) {
    form.append('restUrl', source.restUrl);
  }
  if (source.kind === 'acf-json' && source.acfPath) {
    form.append('acfPath', source.acfPath);
  }
  form.append('options', JSON.stringify(options));
  return form;
}

/**
 * syntheticPreview produces a plausible-looking preview when the API
 * isn't available. The numbers loosely correlate with the source kind
 * so demos feel realistic.
 */
export function syntheticPreview(source: SourceConfig): DryRunResult {
  switch (source.kind) {
    case 'wxr-upload': {
      const sizeMB = (source.wxrFile?.size ?? 0) / (1024 * 1024);
      const scale = Math.max(1, Math.round(sizeMB * 4));
      return {
        authors: Math.max(1, Math.round(scale / 5)),
        categories: Math.max(1, Math.round(scale / 4)),
        tags: scale,
        posts: scale * 3,
        attachments: scale * 2,
        comments: scale * 5,
        warnings: ['Demo preview — API not connected.'],
      };
    }
    case 'rest-url':
      return {
        authors: 3,
        categories: 5,
        tags: 12,
        posts: 25,
        attachments: 18,
        comments: 40,
        warnings: ['Demo preview — API not connected.'],
      };
    case 'acf-json':
      return {
        authors: 0,
        categories: 0,
        tags: 0,
        posts: 0,
        attachments: 0,
        comments: 0,
        warnings: ['ACF JSON migrates field groups only; no posts will land.'],
      };
  }
}
