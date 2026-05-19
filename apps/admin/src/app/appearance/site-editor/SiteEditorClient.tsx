'use client';

/**
 * Site Editor Lite — the main client surface.
 *
 * Two columns:
 *
 *   - Left rail: the list of template parts for the active theme. The
 *     selected part is highlighted; a "Modified" badge marks parts
 *     that have an operator-saved override.
 *   - Right pane: a minimal block editor for the selected part. The
 *     v0.1 cut intentionally ships a *textarea-based* editor — the
 *     operator can hand-edit the JSON BlockTree directly. A richer
 *     `<BlockEditCanvas>`-driven surface lands when the canvas grows
 *     enough block-type coverage to be useful for theme parts
 *     (reserved for a v0.2 follow-up).
 *
 * Autosave fires every 5s on a dirty tree; the status pip shows
 * `idle | saving | saved | error`. A "Reset to theme default" button
 * deletes the override.
 *
 * Error handling: a failed initial fetch surfaces in a banner above
 * the rail with a Retry button — we don't blank the page because the
 * operator may still want to see what they had loaded. Save errors
 * surface in the status pip; the in-memory tree is *not* discarded, so
 * the operator can correct + retry.
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactElement,
} from 'react';
import { ApiError } from '../../api-client';
import { deletePart, fetchParts } from './api';
import type { SiteEditorBlockTree, SiteEditorPart } from './types';
import { useAutosaveOverride } from './useAutosaveOverride';

// `satisfies` keeps the typed literal-key narrowing — every `styles.x`
// access stays a real `CSSProperties` rather than the
// `CSSProperties | undefined` you get with a `Record<string, ...>`
// annotation under `noUncheckedIndexedAccess`.
const styles = {
  root: {
    display: 'grid',
    gridTemplateColumns: '240px 1fr',
    gap: 16,
    minHeight: 480,
  },
  rail: {
    borderRight: '1px solid var(--color-border, #e5e7eb)',
    paddingRight: 12,
  },
  partList: { listStyle: 'none', padding: 0, margin: 0 },
  partItem: {
    padding: '8px 10px',
    borderRadius: 6,
    cursor: 'pointer',
    userSelect: 'none',
    fontSize: 14,
  },
  partItemActive: {
    background: 'var(--color-accent-soft, #eef2ff)',
    color: 'var(--color-accent, #4338ca)',
    fontWeight: 600,
  },
  badge: {
    marginLeft: 8,
    fontSize: 11,
    padding: '1px 6px',
    borderRadius: 4,
    background: 'var(--color-warn-soft, #fef3c7)',
    color: 'var(--color-warn, #92400e)',
  },
  pane: { display: 'flex', flexDirection: 'column', gap: 12 },
  toolbar: {
    display: 'flex',
    gap: 12,
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  status: { fontSize: 13, color: 'var(--color-text-muted, #6b7280)' },
  statusError: { color: 'var(--color-error, #b91c1c)' },
  editor: {
    flex: 1,
    minHeight: 360,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: 13,
    padding: 12,
    borderRadius: 6,
    border: '1px solid var(--color-border, #e5e7eb)',
    resize: 'vertical',
  },
  banner: {
    padding: '8px 12px',
    borderRadius: 6,
    marginBottom: 12,
    background: 'var(--color-error-soft, #fee2e2)',
    color: 'var(--color-error, #b91c1c)',
    fontSize: 13,
  },
  meta: { fontSize: 12, color: 'var(--color-text-muted, #6b7280)' },
} satisfies Record<string, CSSProperties>;

/**
 * Internal shape: a part with an in-memory tree the user is editing.
 * We hold the editing buffer separately so a parse failure (the
 * operator typed invalid JSON) doesn't blow away the textarea — the
 * raw string sticks around in `draft` until the next valid parse.
 */
interface PartState {
  part: SiteEditorPart;
  /** Stringified JSON the operator is editing in the textarea. */
  draft: string;
  /** Parse error, if any, against the current draft. */
  parseError: string | null;
}

function formatTime(d: Date | null): string {
  if (!d) return '';
  return d.toLocaleTimeString();
}

export function SiteEditorClient(): ReactElement {
  const [parts, setParts] = useState<PartState[]>([]);
  const [theme, setTheme] = useState<string>('');
  const [selected, setSelected] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [bannerError, setBannerError] = useState<string | null>(null);

  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    setLoading(true);
    setBannerError(null);
    try {
      const data = await fetchParts(controller.signal);
      setTheme(data.theme);
      const next: PartState[] = data.parts.map((p) => ({
        part: p,
        draft: JSON.stringify(p.blocks, null, 2),
        parseError: null,
      }));
      setParts(next);
      // Pre-select the first part on first load. Preserve the user's
      // selection on subsequent refreshes when possible.
      setSelected((prev) => {
        if (prev && next.some((s) => s.part.name === prev)) return prev;
        return next[0]?.part.name ?? null;
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      const message =
        err instanceof ApiError
          ? `Failed to load parts: ${err.status} ${err.statusText}`
          : err instanceof Error
            ? err.message
            : 'Failed to load parts';
      setBannerError(message);
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    return () => {
      abortRef.current?.abort();
    };
  }, [load]);

  const current = useMemo(
    () => parts.find((p) => p.part.name === selected) ?? null,
    [parts, selected],
  );

  // The tree the autosave hook watches. If the draft fails to parse we
  // hand the last-known-good tree so the autosave doesn't fire with
  // garbage. The dirty-bit comparator inside the hook still sees the
  // tree as up-to-date in that case, which is the right behaviour.
  const currentTree: SiteEditorBlockTree = useMemo(() => {
    if (!current) return [];
    if (current.parseError) return current.part.blocks;
    try {
      return JSON.parse(current.draft) as SiteEditorBlockTree;
    } catch {
      return current.part.blocks;
    }
  }, [current]);

  const autosave = useAutosaveOverride(selected, currentTree, {
    disabled: current === null || current.parseError !== null,
  });

  const onEdit = useCallback(
    (value: string) => {
      if (!selected) return;
      setParts((prev) =>
        prev.map((p) => {
          if (p.part.name !== selected) return p;
          let parseError: string | null = null;
          let blocks = p.part.blocks;
          try {
            const parsed = JSON.parse(value);
            if (!Array.isArray(parsed)) {
              parseError = 'BlockTree must be an array';
            } else {
              blocks = parsed as SiteEditorBlockTree;
            }
          } catch (err) {
            parseError = err instanceof Error ? err.message : 'invalid JSON';
          }
          return {
            ...p,
            draft: value,
            parseError,
            part: parseError ? p.part : { ...p.part, blocks, overridden: true },
          };
        }),
      );
    },
    [selected],
  );

  const onReset = useCallback(async () => {
    if (!selected) return;
    try {
      await deletePart(selected);
      await load();
    } catch (err) {
      const message =
        err instanceof ApiError
          ? `Reset failed: ${err.status} ${err.statusText}`
          : err instanceof Error
            ? err.message
            : 'Reset failed';
      setBannerError(message);
    }
  }, [selected, load]);

  return (
    <section aria-label="Site Editor">
      <header style={styles.toolbar}>
        <div>
          <h1 style={{ marginBottom: 4 }}>Site Editor</h1>
          <p style={styles.meta}>
            Active theme: <strong>{theme || '—'}</strong>. Edit your theme&apos;s template
            parts directly from the browser; changes save automatically.
          </p>
        </div>
      </header>

      {bannerError !== null && (
        <div role="alert" data-testid="site-editor-banner" style={styles.banner}>
          {bannerError}
          <button type="button" onClick={() => void load()} style={{ marginLeft: 12 }}>
            Retry
          </button>
        </div>
      )}

      {loading ? (
        <p data-testid="site-editor-loading" style={styles.meta}>
          Loading parts…
        </p>
      ) : (
        <div style={styles.root}>
          <nav style={styles.rail} aria-label="Template parts">
            <ul style={styles.partList} data-testid="site-editor-rail">
              {parts.map(({ part }) => {
                const active = part.name === selected;
                const itemStyle: CSSProperties = active
                  ? { ...styles.partItem, ...styles.partItemActive }
                  : styles.partItem;
                return (
                  <li
                    key={part.name}
                    style={itemStyle}
                    aria-current={active ? 'page' : undefined}
                    data-testid={`site-editor-part-${part.name}`}
                  >
                    <button
                      type="button"
                      onClick={() => setSelected(part.name)}
                      style={{
                        background: 'none',
                        border: 'none',
                        padding: 0,
                        font: 'inherit',
                        color: 'inherit',
                        cursor: 'pointer',
                        textAlign: 'left',
                        width: '100%',
                      }}
                    >
                      {part.title}
                      {part.overridden && (
                        <span style={styles.badge} data-testid={`site-editor-badge-${part.name}`}>
                          Modified
                        </span>
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          </nav>

          <div style={styles.pane}>
            {current ? (
              <>
                <div style={styles.toolbar}>
                  <div>
                    <h2 style={{ marginBottom: 2 }}>{current.part.title}</h2>
                    <span style={styles.meta}>
                      Area: <strong>{current.part.area || 'uncategorized'}</strong>
                    </span>
                  </div>
                  <div style={styles.toolbar}>
                    <span
                      data-testid="site-editor-status"
                      style={
                        autosave.status === 'error'
                          ? { ...styles.status, ...styles.statusError }
                          : styles.status
                      }
                    >
                      {autosave.status === 'idle' && 'No changes'}
                      {autosave.status === 'saving' && 'Saving…'}
                      {autosave.status === 'saved' &&
                        `Saved ${formatTime(autosave.lastSavedAt)}`}
                      {autosave.status === 'error' && (autosave.error ?? 'Save failed')}
                    </span>
                    <button
                      type="button"
                      onClick={() => void onReset()}
                      disabled={!current.part.overridden}
                      data-testid="site-editor-reset"
                    >
                      Reset to theme default
                    </button>
                  </div>
                </div>

                <textarea
                  aria-label="Block tree JSON"
                  data-testid="site-editor-editor"
                  spellCheck={false}
                  style={styles.editor}
                  value={current.draft}
                  onChange={(e) => onEdit(e.target.value)}
                />

                {current.parseError !== null && (
                  <p
                    role="alert"
                    data-testid="site-editor-parse-error"
                    style={{ ...styles.status, ...styles.statusError }}
                  >
                    {current.parseError}
                  </p>
                )}
              </>
            ) : (
              <p style={styles.meta}>Select a part to edit.</p>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
