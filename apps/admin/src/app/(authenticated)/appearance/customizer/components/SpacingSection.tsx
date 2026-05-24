'use client';

/**
 * SpacingSection — generated spacing scale editor.
 *
 * Drives `settings.spacing.spacingScale`, which the renderer expands
 * into CSS custom properties at theme compile time. The fields map
 * 1:1 to the backend `SpacingScale` struct, so the validator's error
 * messages travel back unchanged when a value is rejected.
 */
import { type ReactElement } from 'react';
import type { SpacingScale } from '../types';

const UNITS: ReadonlyArray<NonNullable<SpacingScale['unit']>> = [
  'px',
  'rem',
  'em',
  '%',
  'vw',
  'vh',
];

export interface SpacingSectionProps {
  scale: SpacingScale;
  onChange: (next: SpacingScale) => void;
}

export function SpacingSection({ scale, onChange }: SpacingSectionProps): ReactElement {
  return (
    <section className="customizer-section" data-testid="customizer-spacing">
      <h2 className="customizer-section__title">Spacing scale</h2>

      <div className="customizer-field" data-path="/settings/spacing/spacingScale/operator">
        <label htmlFor="spacing-operator">Operator</label>
        <select
          id="spacing-operator"
          value={scale.operator ?? '+'}
          onChange={(e) =>
            onChange({ ...scale, operator: e.target.value as SpacingScale['operator'] })
          }
          data-testid="spacing-operator"
        >
          <option value="+">+ (additive)</option>
          <option value="*">* (geometric)</option>
        </select>
      </div>

      <div className="customizer-field" data-path="/settings/spacing/spacingScale/increment">
        <label htmlFor="spacing-increment">Increment</label>
        <input
          id="spacing-increment"
          type="number"
          step="0.1"
          min={0}
          value={scale.increment ?? ''}
          onChange={(e) =>
            onChange({
              ...scale,
              increment: e.target.value === '' ? undefined : Number(e.target.value),
            })
          }
          data-testid="spacing-increment"
        />
      </div>

      <div className="customizer-field" data-path="/settings/spacing/spacingScale/steps">
        <label htmlFor="spacing-steps">Steps</label>
        <input
          id="spacing-steps"
          type="number"
          step="1"
          min={0}
          value={scale.steps ?? ''}
          onChange={(e) =>
            onChange({
              ...scale,
              steps: e.target.value === '' ? undefined : Number(e.target.value),
            })
          }
          data-testid="spacing-steps"
        />
      </div>

      <div className="customizer-field" data-path="/settings/spacing/spacingScale/mediumStep">
        <label htmlFor="spacing-medium">Medium step</label>
        <input
          id="spacing-medium"
          type="number"
          step="0.1"
          min={0}
          value={scale.mediumStep ?? ''}
          onChange={(e) =>
            onChange({
              ...scale,
              mediumStep:
                e.target.value === '' ? undefined : Number(e.target.value),
            })
          }
          data-testid="spacing-medium"
        />
      </div>

      <div className="customizer-field" data-path="/settings/spacing/spacingScale/unit">
        <label htmlFor="spacing-unit">Unit</label>
        <select
          id="spacing-unit"
          value={scale.unit ?? 'rem'}
          onChange={(e) =>
            onChange({ ...scale, unit: e.target.value as SpacingScale['unit'] })
          }
          data-testid="spacing-unit"
        >
          {UNITS.map((u) => (
            <option key={u} value={u}>
              {u}
            </option>
          ))}
        </select>
      </div>
    </section>
  );
}
