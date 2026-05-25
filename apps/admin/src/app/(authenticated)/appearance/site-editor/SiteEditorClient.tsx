'use client';

/**
 * Site Editor Lite — the main client surface.
 *
 * Two columns:
 *
 *   - Left rail: the list of template parts for the active theme on a
 *     `bg-forest` dark surface (mirrors the admin chrome treatment in
 *     docs/design/ui_kits/admin/index.html). The selected part lights
 *     up emerald; modified parts carry a Modified badge.
 *   - Right pane: the brand's paper-2 surface with a code-styled
 *     textarea for the BlockTree JSON. v0.1 ships the textarea editor
 *     so operators can hand-edit the JSON directly; v0.2 swaps in a
 *     richer block canvas once coverage grows.
 *
 * Autosave fires every 5s on a dirty tree; the status pip shows the
 * brand's emerald pulse dot when idle/saved, a yellow dot while saving,
 * a danger dot on failure. "Reset to theme default" deletes the
 * override and refetches.
 *
 * Error handling: a failed initial fetch surfaces in a warning banner
 * above the rail with a Retry button — we don't blank the page because
 * the operator may still want to see what they had loaded. Save errors
 * surface in the status pip; the in-memory tree is *not* discarded, so
 * the operator can correct + retry.
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import { ArrowLeft, FileText, RotateCcw } from 'lucide-react';
import { ApiError } from '@/lib/api-client';
import { Headline } from '@/components/ui/headline';
import { cn } from '@/lib/utils';
import { deletePart, fetchParts } from './api';
import type { SiteEditorBlockTree, SiteEditorPart } from './types';
import { useAutosaveOverride } from './useAutosaveOverride';

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
    <section aria-label="Site Editor" className="flex flex-col gap-6">
      <header className="flex flex-col gap-2 border-b border-border pb-5">
        <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Appearance · Template parts
        </span>
        <Headline as="h1" size="page">
          Template <em>parts</em>.
        </Headline>
        <p className="max-w-[640px] text-md leading-normal text-fg-muted">
          Active theme:{' '}
          <strong className="font-sans font-semibold text-ink">
            {theme || '—'}
          </strong>
          . Edit your theme&apos;s template parts directly from the browser;
          changes save automatically.
        </p>
      </header>

      {bannerError !== null && (
        <div
          role="alert"
          data-testid="site-editor-banner"
          className="flex items-center justify-between gap-3 rounded-md border border-danger bg-danger-soft px-4 py-3 font-sans text-sm text-danger"
        >
          <span>{bannerError}</span>
          <button
            type="button"
            onClick={() => void load()}
            className="inline-flex items-center gap-1 rounded-md border border-danger bg-paper px-3 py-1 font-display text-2xs font-bold text-danger transition-colors hover:bg-danger hover:text-paper"
          >
            Retry
          </button>
        </div>
      )}

      {loading ? (
        <p
          data-testid="site-editor-loading"
          className="font-sans text-sm text-fg-subtle"
        >
          Loading parts…
        </p>
      ) : (
        <div className="grid grid-cols-[280px_1fr] gap-5 min-h-[480px]">
          {/* Forest dark rail — matches the admin chrome treatment. */}
          <nav
            aria-label="Template parts"
            data-surface="forest"
            className="relative overflow-hidden rounded-lg border border-forest-border bg-forest p-4 shadow-md"
          >
            {/* Organic emerald glow on the dark surface — signature brand move. */}
            <div
              aria-hidden
              className="pointer-events-none absolute -right-[40%] -top-[20%] h-[400px] w-[400px]"
              style={{
                background:
                  'radial-gradient(circle, rgba(16, 185, 129, 0.18) 0%, transparent 60%)',
              }}
            />
            <div className="relative">
              <p className="mb-3 font-sans text-2xs font-medium uppercase tracking-[0.14em] text-emerald-bright">
                Parts · {parts.length}
              </p>
              <ul
                className="m-0 flex list-none flex-col gap-1 p-0"
                data-testid="site-editor-rail"
              >
                {parts.map(({ part }) => {
                  const active = part.name === selected;
                  return (
                    <li
                      key={part.name}
                      aria-current={active ? 'page' : undefined}
                      data-testid={`site-editor-part-${part.name}`}
                      className={cn(
                        'rounded-md transition-colors duration-[160ms] ease-brand',
                        active
                          ? 'bg-emerald/15 ring-1 ring-inset ring-emerald-bright/50'
                          : 'hover:bg-forest-3',
                      )}
                    >
                      <button
                        type="button"
                        onClick={() => setSelected(part.name)}
                        className={cn(
                          'flex w-full items-center justify-between gap-2 rounded-md border-0 bg-transparent px-3 py-2 text-left font-sans text-sm',
                          active
                            ? 'font-semibold text-fg-on-forest'
                            : 'font-normal text-fg-on-forest-muted hover:text-fg-on-forest',
                        )}
                      >
                        <span className="flex items-center gap-2">
                          <FileText
                            aria-hidden
                            className={cn(
                              'h-3.5 w-3.5 shrink-0',
                              active
                                ? 'text-emerald-bright'
                                : 'text-fg-on-forest-muted',
                            )}
                          />
                          <span className="truncate">{part.title}</span>
                        </span>
                        {part.overridden && (
                          <span
                            data-testid={`site-editor-badge-${part.name}`}
                            className="inline-flex shrink-0 items-center rounded-sm bg-lavender-soft px-1.5 py-[2px] font-sans text-[10px] font-medium uppercase tracking-wide text-lavender-deep"
                          >
                            Modified
                          </span>
                        )}
                      </button>
                    </li>
                  );
                })}
                {parts.length === 0 && (
                  <li className="px-3 py-2 font-sans text-sm text-fg-on-forest-muted">
                    No parts in this theme yet.
                  </li>
                )}
              </ul>
            </div>
          </nav>

          <div className="flex flex-col gap-4">
            {current ? (
              <>
                <div className="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-border bg-paper-2 p-4 shadow-xs">
                  <div className="flex flex-col gap-1">
                    <h2 className="m-0 font-display text-xl font-bold leading-snug tracking-tight text-ink">
                      {current.part.title}
                    </h2>
                    <span className="font-mono text-xs text-fg-muted">
                      Area:{' '}
                      <strong className="font-mono font-medium text-ink">
                        {current.part.area || 'uncategorized'}
                      </strong>
                    </span>
                  </div>
                  <div className="flex items-center gap-3">
                    <span
                      data-testid="site-editor-status"
                      className={cn(
                        'inline-flex items-center gap-2 rounded-pill px-3 py-1 font-sans text-xs font-medium',
                        autosave.status === 'error'
                          ? 'bg-danger-soft text-danger'
                          : autosave.status === 'saving'
                            ? 'bg-warning-soft text-warning'
                            : 'bg-emerald-soft text-emerald-deep',
                      )}
                    >
                      <AutosaveDot status={autosave.status} />
                      {autosave.status === 'idle' && 'No changes'}
                      {autosave.status === 'saving' && 'Saving…'}
                      {autosave.status === 'saved' &&
                        `Saved ${formatTime(autosave.lastSavedAt)}`}
                      {autosave.status === 'error' &&
                        (autosave.error ?? 'Save failed')}
                    </span>
                    <button
                      type="button"
                      onClick={() => void onReset()}
                      disabled={!current.part.overridden}
                      data-testid="site-editor-reset"
                      className={cn(
                        'inline-flex items-center gap-1.5 rounded-md border font-display text-xs font-bold leading-none transition-colors',
                        'px-3 py-[7px] shadow-xs focus-visible:outline-none focus-visible:shadow-focus',
                        'border-border bg-paper text-ink',
                        'hover:bg-paper-3 hover:border-border-strong',
                        'disabled:cursor-not-allowed disabled:opacity-50',
                      )}
                    >
                      <RotateCcw aria-hidden className="h-3 w-3" />
                      Reset to theme default
                    </button>
                  </div>
                </div>

                <div className="flex flex-col gap-2 rounded-lg border border-border bg-paper-2 p-4 shadow-xs">
                  <div className="flex items-center justify-between">
                    <span className="font-sans text-2xs font-medium uppercase tracking-[0.1em] text-fg-subtle">
                      Block tree · JSON
                    </span>
                    <span className="font-mono text-2xs text-fg-faint">
                      {current.draft.split('\n').length} lines
                    </span>
                  </div>
                  <textarea
                    aria-label="Block tree JSON"
                    data-testid="site-editor-editor"
                    spellCheck={false}
                    value={current.draft}
                    onChange={(e) => onEdit(e.target.value)}
                    className={cn(
                      'min-h-[360px] flex-1 resize-vertical rounded-md border border-border bg-paper p-3 font-mono text-xs leading-relaxed text-ink',
                      'transition-colors duration-[160ms] ease-brand',
                      'hover:border-border-strong',
                      'focus:border-emerald focus:shadow-focus focus:outline-none',
                    )}
                  />

                  {current.parseError !== null && (
                    <p
                      role="alert"
                      data-testid="site-editor-parse-error"
                      className="m-0 inline-flex items-center gap-1.5 rounded-md border border-danger bg-danger-soft px-3 py-2 font-sans text-xs text-danger"
                    >
                      <ArrowLeft aria-hidden className="h-3 w-3" />
                      {current.parseError}
                    </p>
                  )}
                </div>
              </>
            ) : (
              <p className="font-sans text-sm text-fg-subtle">
                Select a part to edit.
              </p>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

/**
 * Autosave indicator dot. The emerald pulse is the brand's signature
 * "alive" beat — see the handoff's `tag--dot` rule. We render the dot
 * inline so the status pip can stay a single inline-flex span.
 */
function AutosaveDot({
  status,
}: {
  status: 'idle' | 'saving' | 'saved' | 'error';
}): ReactElement {
  const cls = cn(
    'inline-block h-2 w-2 shrink-0 rounded-full',
    status === 'error'
      ? 'bg-danger'
      : status === 'saving'
        ? 'bg-warning animate-pulse'
        : 'bg-emerald',
  );
  return <span aria-hidden className={cls} data-testid="site-editor-autosave-dot" />;
}
