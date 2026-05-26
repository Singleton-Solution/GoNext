'use client';

/**
 * SourceStep — wizard step 1.
 *
 * Operator chooses what to import from: a WXR XML upload, a live WP
 * REST URL, or an ACF JSON export on disk. Each kind reveals a single
 * matching input.
 *
 * Validation happens here (before "Next" is clickable) so the next
 * step doesn't have to defend against half-filled state:
 *
 *   - wxr-upload requires a non-null file
 *   - rest-url requires a parseable URL with http(s) scheme
 *   - acf-json requires a non-empty path string
 *
 * Why three options instead of one "auto-detect": the importer treats
 * each source quite differently (streaming XML, paginated REST, file
 * walk) and asking the operator up front avoids wrong-mode failures
 * deep into the wizard.
 */

import { type ReactElement, useCallback, useId } from 'react';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Input } from '@/components/ui/input';
import type { SourceConfig, SourceKind } from '../types';

export interface SourceStepProps {
  value: SourceConfig;
  onChange: (next: SourceConfig) => void;
  onNext: () => void;
}

const KINDS: { kind: SourceKind; label: string; help: string }[] = [
  {
    kind: 'wxr-upload',
    label: 'WXR export (.xml)',
    help: 'The Tools → Export download from a WordPress site.',
  },
  {
    kind: 'rest-url',
    label: 'WordPress REST URL',
    help: 'Live source — the importer pages through /wp-json/wp/v2.',
  },
  {
    kind: 'acf-json',
    label: 'ACF JSON path',
    help: 'For sites where field groups are exported separately.',
  },
];

export function SourceStep({ value, onChange, onNext }: SourceStepProps): ReactElement {
  const kindId = useId();
  const inputId = useId();

  const setKind = useCallback(
    (k: SourceKind) => {
      onChange({ kind: k, wxrFile: null });
    },
    [onChange],
  );

  const ready = isReady(value);

  return (
    <section aria-label="Source selection">
      <h2 className="text-lg font-bold mb-1">1. Source</h2>
      <p className="text-fg-muted text-sm mb-4">
        Pick what the importer reads from.
      </p>
      <fieldset
        className="space-y-2 mb-6"
        aria-labelledby={kindId}
        data-testid="source-kind-group"
      >
        <span id={kindId} className="sr-only">
          Source kind
        </span>
        {KINDS.map((k) => (
          <label
            key={k.kind}
            className={
              'flex items-start gap-3 p-3 rounded-md border cursor-pointer transition-colors ' +
              (value.kind === k.kind
                ? 'border-ink bg-paper-2'
                : 'border-border bg-paper hover:bg-paper-2')
            }
          >
            <input
              type="radio"
              name="source-kind"
              value={k.kind}
              checked={value.kind === k.kind}
              onChange={() => setKind(k.kind)}
              className="mt-1"
              data-testid={`source-kind-${k.kind}`}
            />
            <span>
              <span className="block text-sm font-bold">{k.label}</span>
              <span className="block text-fg-muted text-xs">{k.help}</span>
            </span>
          </label>
        ))}
      </fieldset>

      <div className="mb-6">
        {value.kind === 'wxr-upload' && (
          <>
            <Label htmlFor={inputId}>WXR file</Label>
            <Input
              id={inputId}
              type="file"
              accept=".xml,application/xml,text/xml"
              data-testid="source-wxr-file"
              onChange={(e) =>
                onChange({ ...value, wxrFile: e.target.files?.[0] ?? null })
              }
            />
            {value.wxrFile && (
              <p className="text-xs text-fg-muted mt-1">
                {value.wxrFile.name} ({Math.round(value.wxrFile.size / 1024)} KB)
              </p>
            )}
          </>
        )}
        {value.kind === 'rest-url' && (
          <>
            <Label htmlFor={inputId}>WordPress site URL</Label>
            <Input
              id={inputId}
              type="url"
              placeholder="https://blog.example.com"
              value={value.restUrl ?? ''}
              data-testid="source-rest-url"
              onChange={(e) => onChange({ ...value, restUrl: e.target.value })}
            />
          </>
        )}
        {value.kind === 'acf-json' && (
          <>
            <Label htmlFor={inputId}>ACF JSON path (server-relative)</Label>
            <Input
              id={inputId}
              type="text"
              placeholder="/var/wp/acf-json/group_xyz.json"
              value={value.acfPath ?? ''}
              data-testid="source-acf-path"
              onChange={(e) => onChange({ ...value, acfPath: e.target.value })}
            />
          </>
        )}
      </div>

      <div className="flex justify-end">
        <Button
          onClick={onNext}
          disabled={!ready}
          variant="primary"
          data-testid="source-next"
        >
          Continue
        </Button>
      </div>
    </section>
  );
}

/** isReady gates the "Continue" button on a valid per-kind value. */
export function isReady(c: SourceConfig): boolean {
  switch (c.kind) {
    case 'wxr-upload':
      return c.wxrFile != null;
    case 'rest-url':
      if (!c.restUrl) return false;
      try {
        const u = new URL(c.restUrl);
        return u.protocol === 'http:' || u.protocol === 'https:';
      } catch {
        return false;
      }
    case 'acf-json':
      return Boolean(c.acfPath && c.acfPath.trim() !== '');
  }
}
