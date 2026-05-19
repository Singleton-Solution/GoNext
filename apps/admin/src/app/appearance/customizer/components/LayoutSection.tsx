'use client';

/**
 * LayoutSection — content + wide width editors.
 *
 * The two layout tokens (content / wide) drive the
 * `--gn-layout-content` and `--gn-layout-wide` custom properties.
 * Operators most commonly want pixel values; the inputs accept any
 * CSS length the validator allows.
 */
import { type ReactElement } from 'react';
import type { LayoutSettings } from '../types';

export interface LayoutSectionProps {
  layout: LayoutSettings;
  onChange: (next: LayoutSettings) => void;
}

export function LayoutSection({ layout, onChange }: LayoutSectionProps): ReactElement {
  return (
    <section className="customizer-section" data-testid="customizer-layout">
      <h2 className="customizer-section__title">Layout</h2>
      <div className="customizer-field" data-path="/settings/layout/contentSize">
        <label htmlFor="customizer-content-size">Content width</label>
        <input
          id="customizer-content-size"
          type="text"
          value={layout.contentSize ?? ''}
          placeholder="720px"
          onChange={(e) => onChange({ ...layout, contentSize: e.target.value })}
          data-testid="layout-content-size"
          spellCheck={false}
        />
        <p className="muted">CSS length (e.g. `720px`, `48rem`).</p>
      </div>
      <div className="customizer-field" data-path="/settings/layout/wideSize">
        <label htmlFor="customizer-wide-size">Wide width</label>
        <input
          id="customizer-wide-size"
          type="text"
          value={layout.wideSize ?? ''}
          placeholder="1180px"
          onChange={(e) => onChange({ ...layout, wideSize: e.target.value })}
          data-testid="layout-wide-size"
          spellCheck={false}
        />
        <p className="muted">Width for full-bleed-but-bounded blocks.</p>
      </div>
    </section>
  );
}
