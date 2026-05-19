'use client';

/**
 * CustomizerClient — interactive theme customizer.
 *
 * Owns the local edit state (palette, typography, layout, spacing),
 * the preview iframe URL, and the save / reset side effects. The
 * page server component does the initial fetch; this island takes
 * the result and drives everything from there.
 *
 * Save flow: build the override payload from current state, PUT to
 * the backend, then refresh local state from the response so the
 * preview iframe stops showing the optimistic preview overrides and
 * starts showing the persisted set instead.
 *
 * Reset flow: DELETE the overrides row, then rebuild local state
 * from the (now-empty) override response. The preview iframe re-
 * renders without the override query param.
 */
import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from 'react';
import { ApiError } from '../../api-client';
import { resetOverrides, saveOverrides } from './api';
import { BreakpointEditor } from './components/BreakpointEditor';
import { ColorPicker } from './components/ColorPicker';
import { LayoutGridEditor } from './components/LayoutGridEditor';
import { LayoutSection } from './components/LayoutSection';
import { PreviewFrame } from './components/PreviewFrame';
import { ShadowPresetsEditor } from './components/ShadowPresetsEditor';
import { SpacingScaleEditor } from './components/SpacingScaleEditor';
import { SpacingSection } from './components/SpacingSection';
import { TypographySection } from './components/TypographySection';
import { buildOverrides, initialState, isOverrideEmpty } from './state';
import type {
  ActiveResponse,
  Breakpoint,
  BreakpointSlug,
  ColorEntry,
  FontFamily,
  FontSize,
  LayoutSettings,
  ShadowPreset,
  SpacingScale,
  SpacingSize,
} from './types';

const TOAST_DISMISS_MS = 4_000;

export interface CustomizerClientProps {
  active: ActiveResponse;
  /** Absolute URL to the public site, e.g. http://localhost:3000. The
   *  PreviewFrame iframes this and appends the overrides query. */
  publicSiteUrl: string;
  /** Optional override of the save action. Used by tests to inject a
   *  spy without touching `fetch`. */
  saveAction?: (overrides: ReturnType<typeof buildOverrides>) => Promise<ActiveResponse>;
  /** Optional override of the reset action. Used by tests. */
  resetAction?: () => Promise<void>;
}

type Toast = { kind: 'success' | 'error'; message: string } | null;

export function CustomizerClient({
  active,
  publicSiteUrl,
  saveAction = saveOverrides,
  resetAction = resetOverrides,
}: CustomizerClientProps): ReactElement {
  const [state, setState] = useState(() => initialState(active));
  const [activeResponse, setActiveResponse] = useState(active);
  const [submitting, setSubmitting] = useState(false);
  const [toast, setToast] = useState<Toast>(null);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [activeBreakpoint, setActiveBreakpoint] = useState<BreakpointSlug | null>(
    null,
  );
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Build the preview override payload on every state change. We pass
  // this straight to the iframe even when the diff against the
  // manifest defaults is empty — the renderer treats an empty
  // overrides param the same as no param, so there's no harm in
  // always feeding the iframe the latest value.
  const overrides = useMemo(
    () => buildOverrides(state, activeResponse.theme),
    [state, activeResponse.theme],
  );

  useEffect(() => {
    return () => {
      if (toastTimer.current) clearTimeout(toastTimer.current);
    };
  }, []);

  const showToast = useCallback((next: Toast) => {
    if (toastTimer.current) clearTimeout(toastTimer.current);
    setToast(next);
    if (next) {
      toastTimer.current = setTimeout(() => setToast(null), TOAST_DISMISS_MS);
    }
  }, []);

  // Field-level setters. Each writes a fresh state slice so React's
  // structural equality bailouts work correctly downstream.
  const onPaletteChange = useCallback((index: number, next: ColorEntry) => {
    setState((prev) => {
      const palette = prev.palette.slice();
      palette[index] = next;
      return { ...prev, palette };
    });
  }, []);
  const onFontFamilyChange = useCallback((index: number, next: FontFamily) => {
    setState((prev) => {
      const fontFamilies = prev.fontFamilies.slice();
      fontFamilies[index] = next;
      return { ...prev, fontFamilies };
    });
  }, []);
  const onFontSizeChange = useCallback((index: number, next: FontSize) => {
    setState((prev) => {
      const fontSizes = prev.fontSizes.slice();
      fontSizes[index] = next;
      return { ...prev, fontSizes };
    });
  }, []);
  const onLayoutChange = useCallback(
    (next: LayoutSettings) => setState((prev) => ({ ...prev, layout: next })),
    [],
  );
  const onSpacingChange = useCallback(
    (next: SpacingScale) => setState((prev) => ({ ...prev, spacing: next })),
    [],
  );
  const onSpacingTokensChange = useCallback(
    (next: SpacingSize[]) => setState((prev) => ({ ...prev, spacingTokens: next })),
    [],
  );
  const onShadowPresetsChange = useCallback(
    (next: ShadowPreset[]) => setState((prev) => ({ ...prev, shadowPresets: next })),
    [],
  );
  const onBreakpointsChange = useCallback(
    (next: Breakpoint[]) => setState((prev) => ({ ...prev, breakpoints: next })),
    [],
  );

  // Resolve the iframe width from the active breakpoint selection.
  // Memoised so PreviewFrame's prop identity is stable when the
  // breakpoint hasn't moved.
  const frameWidth = useMemo<number | null>(() => {
    if (activeBreakpoint === null) return null;
    const bp = state.breakpoints.find((b) => b.slug === activeBreakpoint);
    return bp ? bp.width : null;
  }, [activeBreakpoint, state.breakpoints]);

  const dirty = !isOverrideEmpty(overrides);

  async function handleSave(): Promise<void> {
    if (submitting || !dirty) return;
    setSubmitting(true);
    try {
      const updated = await saveAction(overrides);
      setActiveResponse(updated);
      setState(initialState(updated));
      showToast({ kind: 'success', message: 'Theme customizations saved.' });
    } catch (error) {
      const detail =
        error instanceof ApiError && error.payload && typeof error.payload === 'object'
          ? readErrorDetail(error.payload as Record<string, unknown>)
          : error instanceof Error
            ? error.message
            : 'Failed to save customizations.';
      showToast({ kind: 'error', message: detail });
    } finally {
      setSubmitting(false);
    }
  }

  async function handleReset(): Promise<void> {
    if (submitting) return;
    setSubmitting(true);
    try {
      await resetAction();
      const cleared: ActiveResponse = {
        ...activeResponse,
        overrides: {},
      };
      setActiveResponse(cleared);
      setState(initialState(cleared));
      showToast({ kind: 'success', message: 'Theme customizations cleared.' });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : 'Failed to reset customizations.';
      showToast({ kind: 'error', message });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="customizer" data-testid="customizer-root">
      <aside className="customizer__sidebar">
        <header className="customizer__header">
          <h1>Customize</h1>
          <p className="muted">
            Active theme: <strong>{activeResponse.themeSlug}</strong>
          </p>
        </header>

        <section className="customizer-section" data-testid="customizer-palette">
          <h2 className="customizer-section__title">Colors</h2>
          {state.palette.map((entry, i) => (
            <ColorPicker
              key={entry.slug}
              entry={entry}
              path={`/settings/color/palette/${i}/color`}
              onChange={(next) => onPaletteChange(i, next)}
            />
          ))}
          {state.palette.length === 0 && (
            <p className="muted">No palette declared in this theme.</p>
          )}
        </section>

        <TypographySection
          fontFamilies={state.fontFamilies}
          fontSizes={state.fontSizes}
          onFontFamilyChange={onFontFamilyChange}
          onFontSizeChange={onFontSizeChange}
        />
        <LayoutSection layout={state.layout} onChange={onLayoutChange} />
        <SpacingSection scale={state.spacing} onChange={onSpacingChange} />

        <section
          className={`customizer-section customizer-advanced ${
            advancedOpen ? 'is-open' : ''
          }`}
          data-testid="customizer-advanced"
        >
          <button
            type="button"
            className="customizer-advanced__toggle"
            onClick={() => setAdvancedOpen((prev) => !prev)}
            aria-expanded={advancedOpen}
            aria-controls="customizer-advanced-panels"
            data-testid="customizer-advanced-toggle"
          >
            <span className="customizer-section__title">Advanced</span>
            <span className="customizer-advanced__chevron" aria-hidden>
              {advancedOpen ? '−' : '+'}
            </span>
          </button>
          {advancedOpen && (
            <div
              id="customizer-advanced-panels"
              className="customizer-advanced__panels"
            >
              <details className="customizer-advanced__group" open>
                <summary>Spacing scale</summary>
                <SpacingScaleEditor
                  tokens={state.spacingTokens}
                  onChange={onSpacingTokensChange}
                />
              </details>
              <details className="customizer-advanced__group">
                <summary>Shadow presets</summary>
                <ShadowPresetsEditor
                  presets={state.shadowPresets}
                  onChange={onShadowPresetsChange}
                />
              </details>
              <details className="customizer-advanced__group">
                <summary>Layout grid</summary>
                <LayoutGridEditor layout={state.layout} onChange={onLayoutChange} />
              </details>
              <details className="customizer-advanced__group">
                <summary>Breakpoints</summary>
                <BreakpointEditor
                  breakpoints={state.breakpoints}
                  onChange={onBreakpointsChange}
                  activeBreakpoint={activeBreakpoint}
                  onActiveBreakpointChange={setActiveBreakpoint}
                />
              </details>
            </div>
          )}
        </section>

        <div className="customizer__actions">
          <button
            type="button"
            className="btn-primary"
            onClick={handleSave}
            disabled={submitting || !dirty}
            data-testid="customizer-save"
          >
            {submitting ? 'Saving…' : 'Save'}
          </button>
          <button
            type="button"
            className="btn-secondary"
            onClick={handleReset}
            disabled={submitting}
            data-testid="customizer-reset"
          >
            Reset
          </button>
        </div>

        {toast && (
          <div
            className={
              toast.kind === 'success'
                ? 'customizer__toast customizer__toast--success'
                : 'customizer__toast customizer__toast--error'
            }
            role={toast.kind === 'error' ? 'alert' : 'status'}
            data-testid="customizer-toast"
          >
            {toast.message}
          </div>
        )}
      </aside>

      <PreviewFrame
        publicSiteUrl={publicSiteUrl}
        overrides={overrides}
        frameWidth={frameWidth}
      />
    </div>
  );
}

/** Extract a human-readable detail message from a problem+json body.
 *  Falls back to a generic string when the body shape isn't what we
 *  expect — the customizer surface should never throw an unhandled
 *  error at the user. */
function readErrorDetail(payload: Record<string, unknown>): string {
  const detail = payload.detail;
  if (typeof detail === 'string' && detail.length > 0) return detail;
  const title = payload.title;
  if (typeof title === 'string' && title.length > 0) return title;
  return 'Failed to save customizations.';
}
