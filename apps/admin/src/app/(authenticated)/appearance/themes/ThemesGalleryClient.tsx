'use client';

/**
 * Themes umbrella page — installed-theme switcher + drag/drop
 * installer in one surface (issues #13, #18, #65). The page is a
 * server component that hydrates the initial list, then this client
 * component owns the optimistic "Activate" + the upload form.
 *
 * The list portion mirrors the existing ThemeBrowser card grid (same
 * emerald-bright active-badge + paper-2 cards) but pulls real data
 * from /api/v1/admin/themes instead of the hard-coded curated list.
 * Themes that lack a screenshot.png fall back to a CSS preview
 * scene matching the gn-hello starter — that keeps the gallery
 * readable for themes uploaded straight from an editor that doesn't
 * ship a hero image yet.
 *
 * The installer is a drop zone on the same page rather than a
 * separate route: per the issue ("umbrella page combining installer
 * + switcher"), an operator should land on /appearance/themes and
 * see both surfaces. Uploading reruns the list fetch on success so
 * the new card appears immediately.
 */

import { useCallback, useEffect, useState, type ChangeEvent, type DragEvent, type ReactElement } from 'react';
import { ArrowRight, Check, Loader2, Sparkles, Upload } from 'lucide-react';
import { Headline } from '@/components/ui/headline';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { activateTheme, fetchThemesListClient, installTheme } from './api-client';
import type { ThemeInfo } from './types';

interface Props {
  initialThemes: ThemeInfo[];
  initialActiveSlug: string;
}

type Status =
  | { kind: 'idle' }
  | { kind: 'busy'; message: string }
  | { kind: 'success'; message: string }
  | { kind: 'error'; message: string };

export function ThemesGalleryClient({ initialThemes, initialActiveSlug }: Props): ReactElement {
  const [themes, setThemes] = useState(initialThemes);
  const [activeSlug, setActiveSlug] = useState(initialActiveSlug);
  const [busySlug, setBusySlug] = useState<string | null>(null);
  const [installStatus, setInstallStatus] = useState<Status>({ kind: 'idle' });
  const [activateStatus, setActivateStatus] = useState<Status>({ kind: 'idle' });
  const [dragOver, setDragOver] = useState(false);

  const refresh = useCallback(async () => {
    const next = await fetchThemesListClient();
    if (next) {
      setThemes(next.themes);
      setActiveSlug(next.active_slug);
    }
  }, []);

  const onActivate = useCallback(
    async (slug: string) => {
      if (slug === activeSlug) return;
      setBusySlug(slug);
      setActivateStatus({ kind: 'busy', message: `Activating ${slug}…` });
      try {
        await activateTheme(slug);
        setActiveSlug(slug);
        setActivateStatus({ kind: 'success', message: `${slug} is now active.` });
      } catch (err) {
        setActivateStatus({
          kind: 'error',
          message: err instanceof Error ? err.message : 'Activation failed.',
        });
      } finally {
        setBusySlug(null);
      }
    },
    [activeSlug],
  );

  const onUpload = useCallback(
    async (file: File) => {
      if (!/\.(gntheme|zip)$/i.test(file.name)) {
        setInstallStatus({
          kind: 'error',
          message: 'Pick a .gntheme or .zip file.',
        });
        return;
      }
      setInstallStatus({ kind: 'busy', message: `Uploading ${file.name}…` });
      try {
        const result = await installTheme(file);
        setInstallStatus({
          kind: 'success',
          message: `Installed “${result.title}” (${result.slug}).`,
        });
        await refresh();
      } catch (err) {
        setInstallStatus({
          kind: 'error',
          message: err instanceof Error ? err.message : 'Install failed.',
        });
      }
    },
    [refresh],
  );

  // Clear transient status messages after a short delay so the user
  // doesn't see stale toasts the next time they interact.
  useEffect(() => {
    if (activateStatus.kind === 'success' || activateStatus.kind === 'error') {
      const t = setTimeout(() => setActivateStatus({ kind: 'idle' }), 4000);
      return () => clearTimeout(t);
    }
    return undefined;
  }, [activateStatus]);

  return (
    <section
      aria-labelledby="themes-heading"
      data-testid="themes-page"
      className="flex flex-col gap-10 pb-16"
    >
      <header className="flex flex-col gap-3 border-b border-border pb-8">
        <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Appearance · {themes.length} themes installed
        </span>
        <Headline as="h1" size="page" id="themes-heading">
          Themes &amp; <em>installer</em>.
        </Headline>
        <p className="max-w-[640px] text-md leading-normal text-fg-muted">
          Switch the active theme, or drop a <code className="font-mono text-sm">.gntheme</code> archive
          to install a new one. The installer validates the manifest before writing a byte to disk.
        </p>
      </header>

      {/* Installer drop zone — top of the page so it's never hidden behind the gallery. */}
      <InstallDropZone
        status={installStatus}
        dragOver={dragOver}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragOver(false);
          const file = e.dataTransfer.files?.[0];
          if (file) void onUpload(file);
        }}
        onFileSelected={(file) => void onUpload(file)}
      />

      {/* Status banner for the activate flow. Lives between the
          installer and the gallery so it's visible regardless of
          scroll position when the user clicks Activate. */}
      <StatusBanner status={activateStatus} />

      {/* Gallery proper. Same card visual the curated mock used,
          minus the curated frame variants — we render a CSS preview
          frame for themes without screenshot.png. */}
      {themes.length === 0 ? (
        <EmptyState />
      ) : (
        <div
          role="list"
          className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3"
          data-testid="theme-grid"
        >
          {themes.map((theme) => {
            const isActive = theme.slug === activeSlug;
            return (
              <ThemeCard
                key={theme.slug}
                theme={theme}
                isActive={isActive}
                isBusy={busySlug === theme.slug}
                onActivate={() => void onActivate(theme.slug)}
              />
            );
          })}
        </div>
      )}
    </section>
  );
}

interface DropZoneProps {
  status: Status;
  dragOver: boolean;
  onDragOver: (e: DragEvent<HTMLDivElement>) => void;
  onDragLeave: () => void;
  onDrop: (e: DragEvent<HTMLDivElement>) => void;
  onFileSelected: (file: File) => void;
}

function InstallDropZone(props: DropZoneProps): ReactElement {
  const onChange = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) props.onFileSelected(file);
    // Reset so the same file can be picked twice in a row.
    e.target.value = '';
  };
  return (
    <div className="flex flex-col gap-3">
      <div
        data-testid="install-dropzone"
        data-drag-over={props.dragOver || undefined}
        onDragOver={props.onDragOver}
        onDragLeave={props.onDragLeave}
        onDrop={props.onDrop}
        className={cn(
          'relative flex flex-col items-center justify-center gap-3 rounded-lg border-2 border-dashed bg-paper-2 px-6 py-10 text-center transition-colors',
          props.dragOver
            ? 'border-emerald-bright bg-emerald-soft/30'
            : 'border-border hover:border-border-strong',
        )}
      >
        <Upload aria-hidden className="h-7 w-7 text-emerald-deep" />
        <div className="font-display text-lg font-extrabold tracking-tight text-ink">
          Drop a <em className="font-serif font-normal italic text-emerald-deep">.gntheme</em> here
        </div>
        <p className="max-w-[420px] text-sm leading-normal text-fg-muted">
          Or pick a file. The installer validates the <code className="font-mono">theme.json</code>
          {' '}manifest, refuses path-traversal entries, and writes atomically via a rename.
        </p>
        <label className="inline-flex">
          <input
            type="file"
            accept=".gntheme,.zip,application/zip"
            onChange={onChange}
            className="sr-only"
            data-testid="install-file-input"
          />
          <span className="inline-flex cursor-pointer items-center gap-[6px] rounded-md bg-emerald px-3 py-[7px] font-display text-xs font-bold leading-none text-emerald-ink shadow-xs transition-colors hover:bg-emerald-deep hover:text-paper">
            Choose file
          </span>
        </label>
      </div>
      <StatusBanner status={props.status} />
    </div>
  );
}

interface ThemeCardProps {
  theme: ThemeInfo;
  isActive: boolean;
  isBusy: boolean;
  onActivate: () => void;
}

function ThemeCard({ theme, isActive, isBusy, onActivate }: ThemeCardProps): ReactElement {
  return (
    <article
      role="listitem"
      data-testid={`theme-card-${theme.slug}`}
      data-active={isActive || undefined}
      className={cn(
        'group relative flex flex-col overflow-hidden rounded-lg border bg-paper-2 transition-all duration-[160ms] ease-brand',
        'shadow-xs hover:-translate-y-[2px] hover:shadow-md',
        isActive
          ? 'border-emerald-bright ring-2 ring-emerald-bright/35'
          : 'border-border hover:border-border-strong',
      )}
    >
      {isActive && (
        <span
          data-testid={`theme-badge-${theme.slug}`}
          className="absolute right-3 top-3 z-10 inline-flex items-center gap-1 rounded-pill bg-emerald px-2 py-[3px] font-sans text-2xs font-semibold uppercase tracking-wider text-emerald-ink shadow-xs"
        >
          <Sparkles aria-hidden className="h-3 w-3" />
          Active
        </span>
      )}

      <ThemePreview slug={theme.slug} hasScreenshot={theme.has_screenshot} />

      <div className="flex flex-1 flex-col gap-3 p-5">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="font-display text-xl font-bold tracking-tight text-ink">{theme.title}</h2>
          <span className="font-mono text-2xs uppercase tracking-[0.08em] text-fg-subtle">
            v{theme.version}
          </span>
        </div>
        <p className="text-sm leading-normal text-fg-muted">
          {theme.description || (
            <>
              Slug{' '}
              <code className="rounded-sm bg-paper-3 px-1 py-px font-mono text-xs text-fg-muted">
                {theme.slug}
              </code>
            </>
          )}
        </p>

        <div className="mt-auto flex items-center justify-end border-t border-border pt-4">
          <Button
            type="button"
            onClick={onActivate}
            disabled={isActive || isBusy}
            data-testid={`theme-activate-${theme.slug}`}
            size="sm"
            className={cn(
              'inline-flex items-center gap-[6px]',
              isActive && 'cursor-default bg-emerald-soft text-emerald-deep hover:bg-emerald-soft',
            )}
            aria-label={isActive ? `${theme.title} is the active theme` : `Activate ${theme.title}`}
          >
            {isBusy ? (
              <>
                <Loader2 aria-hidden className="h-3 w-3 animate-spin" />
                Working
              </>
            ) : isActive ? (
              <>
                <Check aria-hidden className="h-3 w-3" />
                Active
              </>
            ) : (
              <>
                Activate
                <ArrowRight aria-hidden className="h-3 w-3" />
              </>
            )}
          </Button>
        </div>
      </div>
    </article>
  );
}

function ThemePreview({ slug, hasScreenshot }: { slug: string; hasScreenshot: boolean }): ReactElement {
  const wrapperClass =
    'relative aspect-[4/3] overflow-hidden border-b border-border bg-paper';
  // If the theme ships a screenshot.png we surface it via the dedicated
  // proxy endpoint a future server handler can serve; until then,
  // every theme falls back to the CSS scene.
  if (hasScreenshot) {
    return (
      <div className={wrapperClass} data-testid={`theme-screenshot-${slug}`}>
        <div className="flex h-full items-center justify-center bg-paper-3 text-2xs uppercase tracking-[0.14em] text-fg-muted">
          {slug}
        </div>
      </div>
    );
  }
  return (
    <div className={wrapperClass} data-testid={`theme-fallback-${slug}`}>
      <div className="absolute inset-3 flex flex-col overflow-hidden rounded-sm border border-border bg-paper-2 shadow-sm">
        <div className="flex gap-1 border-b border-border bg-paper-3 px-2 py-[5px]">
          <span className="block h-[6px] w-[6px] rounded-full bg-fg-faint/60" />
          <span className="block h-[6px] w-[6px] rounded-full bg-fg-faint/60" />
          <span className="block h-[6px] w-[6px] rounded-full bg-fg-faint/60" />
        </div>
        <div className="flex flex-1 flex-col gap-1.5 bg-paper p-3.5">
          <span className="font-sans text-[8px] font-medium uppercase tracking-[0.14em] text-emerald-deep">
            {slug}
          </span>
          <span className="font-display text-[18px] font-extrabold leading-none tracking-tight text-ink">
            A <em className="font-serif font-normal italic text-emerald-deep">living</em> theme.
          </span>
          <div className="mt-1 h-[5px] w-4/5 rounded-[2px] bg-paper-3" />
          <div className="h-[5px] w-3/5 rounded-[2px] bg-paper-3" />
          <div className="h-[5px] w-1/2 rounded-[2px] bg-paper-3" />
        </div>
      </div>
    </div>
  );
}

function EmptyState(): ReactElement {
  return (
    <div
      data-testid="themes-empty"
      className="flex flex-col items-center gap-3 rounded-lg border border-dashed border-border bg-paper-2 px-6 py-12 text-center"
    >
      <Headline as="h2" size="sub" className="text-ink">
        No themes <em>yet</em>.
      </Headline>
      <p className="max-w-[420px] text-sm text-fg-muted">
        Drop a <code className="font-mono">.gntheme</code> archive into the zone above to get
        started. The seeder will normally provision <code className="font-mono">gn-hello</code>
        on first boot.
      </p>
    </div>
  );
}

function StatusBanner({ status }: { status: Status }): ReactElement | null {
  if (status.kind === 'idle') return null;
  return (
    <div
      role={status.kind === 'error' ? 'alert' : 'status'}
      data-testid={`themes-status-${status.kind}`}
      className={cn(
        'flex items-center gap-2 rounded-md border px-4 py-2 font-sans text-sm',
        status.kind === 'busy' && 'border-border bg-paper-2 text-fg-muted',
        status.kind === 'success' && 'border-emerald-bright/40 bg-emerald-soft text-emerald-deep',
        status.kind === 'error' && 'border-rose-300 bg-rose-50 text-rose-900',
      )}
    >
      {status.kind === 'busy' && <Loader2 aria-hidden className="h-4 w-4 animate-spin" />}
      {status.kind === 'success' && <Check aria-hidden className="h-4 w-4" />}
      <span>{status.message}</span>
    </div>
  );
}
