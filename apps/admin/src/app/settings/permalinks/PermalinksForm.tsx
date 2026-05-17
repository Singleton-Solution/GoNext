'use client';

/**
 * Permalinks form with a live preview.
 *
 * The preview is computed from the format string the user is currently
 * typing — `%postname%` → `hello-world`, `%year%` → `2026`, etc. It does
 * NOT round-trip through the API; we just want to give the user a feel
 * for what their format will produce before they save.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { Setting, SettingsValues } from '../types';

export const PERMALINKS_SCHEMA: readonly Setting[] = [
  {
    key: 'core.permalinks.format',
    label: 'Permalink format',
    type: 'text',
    required: true,
    placeholder: '/%year%/%monthnum%/%postname%',
    help: 'Tokens: %year%, %monthnum%, %day%, %postname%, %category%, %author%.',
  },
];

const SAMPLE_VALUES: Record<string, string> = {
  '%year%': '2026',
  '%monthnum%': '05',
  '%day%': '17',
  '%postname%': 'hello-world',
  '%category%': 'news',
  '%author%': 'jane',
};

function renderPreview(format: string): string {
  if (!format) return '(empty)';
  let out = format;
  for (const [token, value] of Object.entries(SAMPLE_VALUES)) {
    out = out.split(token).join(value);
  }
  return out;
}

export interface PermalinksFormProps {
  initialValues: SettingsValues;
  banner?: string;
}

export function PermalinksForm({
  initialValues,
  banner,
}: PermalinksFormProps): ReactElement {
  return (
    <SettingsForm
      schema={PERMALINKS_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
      renderExtras={(values) => {
        const raw = values['core.permalinks.format'];
        const format = typeof raw === 'string' ? raw : '';
        return (
          <div className="permalinks-preview" aria-live="polite">
            <strong>Preview:</strong>{' '}
            <code data-testid="permalinks-preview">{renderPreview(format)}</code>
          </div>
        );
      }}
    />
  );
}
