'use client';

/**
 * OptionsStep — wizard step 2.
 *
 * Three options live here:
 *
 *   - Media mode: copy vs. proxy (issue #187). Default 'copy'. Proxy
 *     is faster but binds the GoNext site to the source's uptime.
 *   - Shortcode mode: keep | strip | convert. Default 'convert'.
 *   - Role overrides: a small editor for WP-role → GoNext-role
 *     remapping. We pre-populate the defaults the importer would use
 *     so operators can see what they're starting from.
 *
 * The role editor keeps a single editable row at the bottom and an
 * "Add override" button so the operator can keep stacking entries.
 * Removing a row drops it from the map; an empty WP key drops the
 * row on next render.
 */

import {
  type ReactElement,
  useCallback,
  useId,
  useState,
} from 'react';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Input } from '@/components/ui/input';
import type { MediaMode, OptionsConfig, ShortcodeMode } from '../types';

export interface OptionsStepProps {
  value: OptionsConfig;
  onChange: (next: OptionsConfig) => void;
  onBack: () => void;
  onNext: () => void;
}

const MEDIA_OPTIONS: { value: MediaMode; label: string; help: string }[] = [
  {
    value: 'copy',
    label: 'Copy media',
    help: 'Download every attachment into GoNext storage. Safer.',
  },
  {
    value: 'proxy',
    label: 'Proxy media',
    help: 'Rewrite URLs; the source server still hosts the files.',
  },
];

const SHORTCODE_OPTIONS: { value: ShortcodeMode; label: string; help: string }[] = [
  {
    value: 'convert',
    label: 'Convert to blocks',
    help: 'Map known shortcodes to core blocks; unknown ones stay as text.',
  },
  {
    value: 'keep',
    label: 'Keep raw',
    help: 'Preserve [shortcode] markers verbatim.',
  },
  {
    value: 'strip',
    label: 'Strip',
    help: 'Remove all shortcode markers from content.',
  },
];

export function OptionsStep({
  value,
  onChange,
  onBack,
  onNext,
}: OptionsStepProps): ReactElement {
  const mediaId = useId();
  const shortcodeId = useId();
  const [draftKey, setDraftKey] = useState('');
  const [draftVal, setDraftVal] = useState('');

  const addOverride = useCallback(() => {
    const k = draftKey.trim();
    const v = draftVal.trim();
    if (!k || !v) return;
    onChange({
      ...value,
      roleOverrides: { ...value.roleOverrides, [k]: v },
    });
    setDraftKey('');
    setDraftVal('');
  }, [draftKey, draftVal, value, onChange]);

  const removeOverride = useCallback(
    (k: string) => {
      const next = { ...value.roleOverrides };
      delete next[k];
      onChange({ ...value, roleOverrides: next });
    },
    [value, onChange],
  );

  return (
    <section aria-label="Options">
      <h2 className="text-lg font-bold mb-1">2. Options</h2>
      <p className="text-fg-muted text-sm mb-6">
        Defaults are safe — change only what you need.
      </p>

      <fieldset className="mb-6" aria-labelledby={mediaId} data-testid="media-mode-group">
        <legend id={mediaId} className="font-bold text-sm mb-2">
          Media mode
        </legend>
        <div className="space-y-2">
          {MEDIA_OPTIONS.map((o) => (
            <label
              key={o.value}
              className={
                'flex items-start gap-3 p-3 rounded-md border cursor-pointer ' +
                (value.mediaMode === o.value
                  ? 'border-ink bg-paper-2'
                  : 'border-border bg-paper hover:bg-paper-2')
              }
            >
              <input
                type="radio"
                name="media-mode"
                value={o.value}
                checked={value.mediaMode === o.value}
                onChange={() => onChange({ ...value, mediaMode: o.value })}
                data-testid={`media-${o.value}`}
              />
              <span>
                <span className="block text-sm font-bold">{o.label}</span>
                <span className="block text-fg-muted text-xs">{o.help}</span>
              </span>
            </label>
          ))}
        </div>
      </fieldset>

      <fieldset className="mb-6" aria-labelledby={shortcodeId} data-testid="shortcode-mode-group">
        <legend id={shortcodeId} className="font-bold text-sm mb-2">
          Shortcode handling
        </legend>
        <div className="space-y-2">
          {SHORTCODE_OPTIONS.map((o) => (
            <label
              key={o.value}
              className={
                'flex items-start gap-3 p-3 rounded-md border cursor-pointer ' +
                (value.shortcodeMode === o.value
                  ? 'border-ink bg-paper-2'
                  : 'border-border bg-paper hover:bg-paper-2')
              }
            >
              <input
                type="radio"
                name="shortcode-mode"
                value={o.value}
                checked={value.shortcodeMode === o.value}
                onChange={() => onChange({ ...value, shortcodeMode: o.value })}
                data-testid={`shortcode-${o.value}`}
              />
              <span>
                <span className="block text-sm font-bold">{o.label}</span>
                <span className="block text-fg-muted text-xs">{o.help}</span>
              </span>
            </label>
          ))}
        </div>
      </fieldset>

      <div className="mb-6">
        <h3 className="font-bold text-sm mb-2">Role overrides</h3>
        <p className="text-fg-muted text-xs mb-3">
          The defaults are sensible (administrator → admin, editor → editor).
          Add an override only when a WP role maps to a non-obvious GoNext
          role.
        </p>
        {Object.entries(value.roleOverrides).length > 0 && (
          <ul className="space-y-1 mb-3" data-testid="role-override-list">
            {Object.entries(value.roleOverrides).map(([k, v]) => (
              <li
                key={k}
                className="flex items-center justify-between text-sm bg-paper-2 rounded-md px-3 py-1.5"
              >
                <span>
                  <code className="bg-paper-3 rounded px-1">{k}</code>
                  {' → '}
                  <code className="bg-paper-3 rounded px-1">{v}</code>
                </span>
                <button
                  type="button"
                  className="text-xs text-danger underline"
                  onClick={() => removeOverride(k)}
                  data-testid={`role-remove-${k}`}
                >
                  Remove
                </button>
              </li>
            ))}
          </ul>
        )}
        <div className="flex gap-2 items-end">
          <div className="flex-1">
            <Label htmlFor="role-key">WP role slug</Label>
            <Input
              id="role-key"
              value={draftKey}
              onChange={(e) => setDraftKey(e.target.value)}
              placeholder="contributor"
              data-testid="role-key-input"
            />
          </div>
          <div className="flex-1">
            <Label htmlFor="role-val">GoNext role slug</Label>
            <Input
              id="role-val"
              value={draftVal}
              onChange={(e) => setDraftVal(e.target.value)}
              placeholder="contributor"
              data-testid="role-val-input"
            />
          </div>
          <Button
            type="button"
            onClick={addOverride}
            variant="default"
            data-testid="role-add"
          >
            Add
          </Button>
        </div>
      </div>

      <div className="flex justify-between">
        <Button onClick={onBack} variant="ghost" data-testid="options-back">
          Back
        </Button>
        <Button onClick={onNext} variant="primary" data-testid="options-next">
          Continue
        </Button>
      </div>
    </section>
  );
}
