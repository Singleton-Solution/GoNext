'use client';

/**
 * BreakpointEditor — advanced editor for the four responsive
 * breakpoints (sm, md, lg, xl).
 *
 * Each breakpoint has a px input that drives the min-width threshold
 * the renderer uses to gate the matching CSS media query. The
 * "active breakpoint" radio decides which width the preview iframe
 * (in `CustomizerClient`) renders at — designers click sm → the
 * iframe collapses to 640 px so they can confirm the small-screen
 * layout looks right.
 *
 * The activeBreakpoint callback is fired with `null` to clear the
 * lock (return to full-width preview).
 */
import { type ReactElement } from 'react';
import type { Breakpoint, BreakpointSlug } from '../types';
import { TokenPreview } from './TokenPreview';

export interface BreakpointEditorProps {
  breakpoints: Breakpoint[];
  onChange: (next: Breakpoint[]) => void;
  activeBreakpoint: BreakpointSlug | null;
  onActiveBreakpointChange: (slug: BreakpointSlug | null) => void;
}

export function BreakpointEditor({
  breakpoints,
  onChange,
  activeBreakpoint,
  onActiveBreakpointChange,
}: BreakpointEditorProps): ReactElement {
  function updateBreakpoint(index: number, next: Breakpoint): void {
    const updated = breakpoints.slice();
    updated[index] = next;
    onChange(updated);
  }

  return (
    <div className="customizer-advanced__panel" data-testid="breakpoint-editor">
      <p className="muted">
        Click a breakpoint to lock the preview iframe to its width. Click
        “Full width” to return to a normal preview.
      </p>
      <div className="breakpoint-editor__active-row">
        <button
          type="button"
          className={`breakpoint-editor__active-btn ${
            activeBreakpoint === null ? 'is-active' : ''
          }`}
          onClick={() => onActiveBreakpointChange(null)}
          data-testid="breakpoint-active-full"
          aria-pressed={activeBreakpoint === null}
        >
          Full width
        </button>
        {breakpoints.map((bp) => (
          <button
            key={bp.slug}
            type="button"
            className={`breakpoint-editor__active-btn ${
              activeBreakpoint === bp.slug ? 'is-active' : ''
            }`}
            onClick={() => onActiveBreakpointChange(bp.slug)}
            data-testid={`breakpoint-active-${bp.slug}`}
            aria-pressed={activeBreakpoint === bp.slug}
          >
            {bp.slug.toUpperCase()} · {bp.width}px
          </button>
        ))}
      </div>
      <div className="breakpoint-editor__list">
        {breakpoints.map((bp, i) => (
          <div
            key={bp.slug}
            className="breakpoint-editor__row"
            data-path={`/settings/custom/gonext/breakpoints/${i}`}
            data-slug={bp.slug}
          >
            <div className="breakpoint-editor__meta">
              <span className="breakpoint-editor__slug">{bp.slug}</span>
              <span className="breakpoint-editor__name">{bp.name}</span>
            </div>
            <div className="breakpoint-editor__input">
              <label htmlFor={`breakpoint-${bp.slug}`}>min-width (px)</label>
              <input
                id={`breakpoint-${bp.slug}`}
                type="number"
                min={0}
                step={1}
                value={bp.width}
                onChange={(e) =>
                  updateBreakpoint(i, { ...bp, width: Number(e.target.value) || 0 })
                }
                aria-label={`${bp.name} breakpoint width`}
                data-testid={`breakpoint-input-${bp.slug}`}
              />
            </div>
            <TokenPreview
              slug={bp.slug}
              label={bp.slug}
              detail={`${bp.width}px`}
              mode="bar"
              px={bp.width}
              viewportPx={1440}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

/** Look up a breakpoint by slug. Returns null when the slug doesn't
 *  match any entry — used by CustomizerClient to derive the iframe
 *  width attribute from the active selection. */
export function findBreakpoint(
  breakpoints: Breakpoint[],
  slug: BreakpointSlug | null,
): Breakpoint | null {
  if (slug === null) return null;
  return breakpoints.find((b) => b.slug === slug) ?? null;
}

export const __testing = { findBreakpoint };
