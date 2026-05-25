'use client';

/**
 * Permalinks form with preset radio cards + a live preview.
 *
 * The preset row sits above the custom-structure input. Picking a preset
 * writes its `format` into the underlying field; typing a value that
 * matches one of the presets re-highlights that card. The structure
 * input is mono-styled (Geist Mono) so the URL template reads like code.
 *
 * The preview is computed from the format string the user is currently
 * typing — `%postname%` → `hello-world`, `%year%` → `2026`, etc. It does
 * NOT round-trip through the API; we just want to give the user a feel
 * for what their format will produce before they save.
 */
import { useId, type ReactElement } from 'react';

import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { Setting, SettingsSection, SettingsValues } from '../types';

export const PERMALINKS_SCHEMA: readonly Setting[] = [
  {
    key: 'core.permalinks.format',
    label: 'Custom structure',
    type: 'text',
    required: true,
    placeholder: '/%year%/%monthnum%/%postname%',
    mono: true,
    help: 'Tokens: %year%, %monthnum%, %day%, %postname%, %category%, %author%.',
  },
];

const SECTIONS: readonly SettingsSection[] = [
  {
    title: 'Custom structure',
    description: 'Compose the URL template directly with tokens.',
    keys: ['core.permalinks.format'],
  },
];

interface Preset {
  /** Stable identifier used as the radio value. */
  id: string;
  /** Human-readable label rendered on the card. */
  label: string;
  /** Format string written into the form when picked. */
  format: string;
  /** Rendered URL shown under the label as a mono preview. */
  preview: string;
}

const PRESETS: readonly Preset[] = [
  { id: 'plain',     label: 'Plain',         format: '/?p=%post_id%',           preview: '/?p=123' },
  { id: 'day',       label: 'Day and name',  format: '/%year%/%monthnum%/%day%/%postname%', preview: '/2026/05/17/hello-world' },
  { id: 'month',     label: 'Month and name',format: '/%year%/%monthnum%/%postname%',       preview: '/2026/05/hello-world' },
  { id: 'numeric',   label: 'Numeric',       format: '/archives/%post_id%',     preview: '/archives/123' },
  { id: 'name',      label: 'Post name',     format: '/%postname%',             preview: '/hello-world' },
];

const SAMPLE_VALUES: Record<string, string> = {
  '%year%': '2026',
  '%monthnum%': '05',
  '%day%': '17',
  '%postname%': 'hello-world',
  '%category%': 'news',
  '%author%': 'jane',
  '%post_id%': '123',
};

function renderPreview(format: string): string {
  if (!format) return '(empty)';
  let out = format;
  for (const [token, value] of Object.entries(SAMPLE_VALUES)) {
    out = out.split(token).join(value);
  }
  return out;
}

function matchPreset(format: string): string | null {
  const hit = PRESETS.find((p) => p.format === format);
  return hit ? hit.id : null;
}

export interface PermalinksFormProps {
  initialValues: SettingsValues;
  banner?: string;
}

export function PermalinksForm({
  initialValues,
  banner,
}: PermalinksFormProps): ReactElement {
  // Stable id-prefix so the radio group keeps unique ids when the page
  // re-renders. `useId` is React 18+ — already in use elsewhere in admin.
  const groupId = useId();
  return (
    <SettingsForm
      schema={PERMALINKS_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
      sections={SECTIONS}
      renderExtras={(values, setValue) => {
        const raw = values['core.permalinks.format'];
        const format = typeof raw === 'string' ? raw : '';
        const activePreset = matchPreset(format);
        return (
          <>
            <section className="settings-section">
              <header className="settings-section__head">
                <h2 className="settings-section__title">Structure presets</h2>
                <p className="settings-section__sub">
                  Pick a common format. The custom structure above updates
                  to match.
                </p>
              </header>

              <fieldset
                className="permalinks-presets"
                data-testid="permalinks-presets"
              >
                <legend className="sr-only">Permalink structure preset</legend>
                {PRESETS.map((preset) => {
                  const radioId = `${groupId}-${preset.id}`;
                  const checked = activePreset === preset.id;
                  return (
                    <label
                      key={preset.id}
                      htmlFor={radioId}
                      className="permalinks-preset"
                      data-active={checked ? 'true' : undefined}
                    >
                      <input
                        id={radioId}
                        type="radio"
                        name={`${groupId}-preset`}
                        value={preset.id}
                        checked={checked}
                        className="permalinks-preset__radio"
                        onChange={() => {
                          // Picking a preset writes its format into the
                          // shared form state — the custom-structure
                          // input re-renders with the new value.
                          setValue('core.permalinks.format', preset.format);
                        }}
                      />
                      <span className="permalinks-preset__body">
                        <span className="permalinks-preset__label">
                          {preset.label}
                        </span>
                        <code className="permalinks-preset__url">
                          {preset.preview}
                        </code>
                      </span>
                    </label>
                  );
                })}
              </fieldset>
            </section>

            <div className="permalinks-preview" aria-live="polite">
              <strong>Preview</strong>
              <code data-testid="permalinks-preview">
                {renderPreview(format)}
              </code>
            </div>
          </>
        );
      }}
    />
  );
}
