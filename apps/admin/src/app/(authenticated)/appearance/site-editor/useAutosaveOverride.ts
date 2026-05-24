/**
 * `useAutosaveOverride` — periodic background save for a single
 * template-part override.
 *
 * The hook is a near-copy of `useAutosave` from
 * `@gonext/blocks-editor` (#384) — same dirty-bit + debounce + abort
 * machinery — but scoped to the site-editor's PUT endpoint and
 * configured with a 5-second interval per the design brief (template
 * parts are smaller and change more deliberately than a 30s autosave
 * on a long post).
 *
 * We don't import `useAutosave` directly because that hook hard-codes
 * `${endpointBase}/${postId}/autosave`, which is the wrong shape for
 * `PUT /api/v1/admin/site_editor/parts/{name}`. Rather than thread a
 * second `path` knob through the shared hook, we mirror its proven
 * cleanup pattern here — the duplication is small and the seam is
 * worth it for a one-off endpoint shape.
 */
'use client';

import { useEffect, useRef, useState } from 'react';
import { ApiError } from '@/lib/api-client';
import { putPart } from './api';
import type { SiteEditorBlockTree } from './types';

/** Default tick. Brief calls for "5s interval" on the override path. */
const DEFAULT_INTERVAL_MS = 5_000;
/** Default debounce — short, so a single edit lands quickly. */
const DEFAULT_DEBOUNCE_MS = 750;

export type AutosaveOverrideStatus = 'idle' | 'saving' | 'saved' | 'error';

export interface AutosaveOverrideState {
  status: AutosaveOverrideStatus;
  lastSavedAt: Date | null;
  error: string | null;
}

export interface UseAutosaveOverrideOptions {
  /** How often the timer fires when blocks are dirty. */
  intervalMs?: number;
  /** Debounce window so rapid edits collapse into one save. */
  debounceMs?: number;
  /** Skip autosave entirely. The page sets this when no part is selected. */
  disabled?: boolean;
  /**
   * Injection seam for tests — a stub `putPart` that doesn't fire a
   * real fetch. Defaults to the real implementation.
   */
  putImpl?: typeof putPart;
}

function serialise(blocks: SiteEditorBlockTree): string {
  return JSON.stringify(blocks);
}

/**
 * Autosave the override for `name` whenever `blocks` changes. Returns
 * the lifecycle state for the UI's status pip.
 */
export function useAutosaveOverride(
  name: string | null,
  blocks: SiteEditorBlockTree,
  opts: UseAutosaveOverrideOptions = {},
): AutosaveOverrideState {
  const intervalMs = opts.intervalMs ?? DEFAULT_INTERVAL_MS;
  const debounceMs = opts.debounceMs ?? DEFAULT_DEBOUNCE_MS;
  const disabled = opts.disabled ?? false;
  const putImpl = opts.putImpl ?? putPart;

  const [state, setState] = useState<AutosaveOverrideState>({
    status: 'idle',
    lastSavedAt: null,
    error: null,
  });

  // Track the last *serialised* snapshot per part so switching parts
  // doesn't trigger a save of the new part on first render.
  const lastSavedSerialised = useRef<Record<string, string>>({});
  const abortRef = useRef<AbortController | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Reset the dirty bit when the selected part changes — the new tree
  // is the new baseline, not an edit to be saved.
  useEffect(() => {
    if (!name) return;
    lastSavedSerialised.current[name] ??= serialise(blocks);
  }, [name, blocks]);

  // Effect: schedule a save when blocks change.
  useEffect(() => {
    if (disabled || !name) return undefined;

    const snapshot = serialise(blocks);
    const baseline = lastSavedSerialised.current[name];
    if (baseline === undefined) {
      lastSavedSerialised.current[name] = snapshot;
      return undefined;
    }
    if (baseline === snapshot) {
      return undefined;
    }

    if (timerRef.current !== null) clearTimeout(timerRef.current);
    const total = Math.max(debounceMs, intervalMs);

    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      const fresh = serialise(blocks);
      // Capture the current name so a part-switch mid-flight doesn't
      // write the wrong override.
      const targetName = name;
      void (async () => {
        abortRef.current?.abort();
        const controller = new AbortController();
        abortRef.current = controller;

        setState((s) => ({ ...s, status: 'saving' }));
        try {
          await putImpl(targetName, { blocks }, controller.signal);
          lastSavedSerialised.current[targetName] = fresh;
          setState({ status: 'saved', lastSavedAt: new Date(), error: null });
        } catch (err) {
          if (controller.signal.aborted) return;
          const message =
            err instanceof ApiError
              ? `${err.status}: ${typeof err.payload === 'object' && err.payload && 'detail' in err.payload ? String((err.payload as { detail: unknown }).detail) : err.message}`
              : err instanceof Error
                ? err.message
                : String(err);
          setState((s) => ({ ...s, status: 'error', error: message }));
        } finally {
          if (abortRef.current === controller) abortRef.current = null;
        }
      })();
    }, total);

    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [name, blocks, debounceMs, intervalMs, disabled, putImpl]);

  // Unmount: abort any in-flight save + cancel pending timer.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      abortRef.current = null;
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    };
  }, []);

  return state;
}
