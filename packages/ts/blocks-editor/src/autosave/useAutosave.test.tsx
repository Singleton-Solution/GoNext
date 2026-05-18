/**
 * Tests for `useAutosave`.
 *
 * We exercise the three lifecycle paths:
 *
 *  - The timer fires at the configured interval and POSTs the
 *    current blocks.
 *  - The save is *skipped* when blocks haven't changed since the
 *    last save.
 *  - Unmount aborts any in-flight request.
 *
 * Plus a couple of error-path checks (423 surfaces an error; non-OK
 * status surfaces an error).
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useAutosave } from './useAutosave.ts';
import type { BlockTree } from '@gonext/blocks-sdk';

const blocksA: BlockTree = [
  { type: 'core/paragraph', attributes: { text: 'hi' }, innerBlocks: [] },
];
const blocksB: BlockTree = [
  { type: 'core/paragraph', attributes: { text: 'bye' }, innerBlocks: [] },
];

beforeEach(() => {
  // shouldAdvanceTime lets React's microtask flush via the still-real
  // queueMicrotask path while setTimeout/setInterval are faked — that
  // combination is what lets renderHook(...) + advanceTimersByTimeAsync
  // settle without deadlocking on a manual `await Promise.resolve()`.
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

/**
 * Drain pending microtasks AND any zero-delay timers. Using fake
 * timers means awaiting a fetch-mock promise alone isn't enough —
 * the inner `then` callbacks queue against the fake scheduler too.
 */
async function flush(): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}

/**
 * Build a fetch mock that records calls and resolves with the given
 * status + body. The body is JSON-stringified before reply so the
 * hook's await of `res.json()` works.
 */
function makeFetchMock(responses: { status: number; body?: unknown }[]): {
  fn: typeof fetch;
  calls: { url: string; body: unknown }[];
} {
  const calls: { url: string; body: unknown }[] = [];
  let i = 0;
  const fn = vi.fn(async (url: string, init?: RequestInit) => {
    const body = init?.body !== undefined ? JSON.parse(String(init.body)) : null;
    calls.push({ url, body });
    const r =
      responses[Math.min(i, responses.length - 1)] ?? { status: 200 };
    i++;
    return {
      ok: r.status >= 200 && r.status < 300,
      status: r.status,
      json: async () => r.body ?? {},
    } as unknown as Response;
  }) as unknown as typeof fetch;
  return { fn, calls };
}

describe('useAutosave', () => {
  it('fires a POST after intervalMs when blocks change', async () => {
    const { fn, calls } = makeFetchMock([{ status: 200, body: {} }]);
    const { result, rerender } = renderHook(
      ({ blocks }: { blocks: BlockTree }) =>
        useAutosave('post-1', blocks, {
          intervalMs: 5000,
          debounceMs: 100,
          fetchImpl: fn,
        }),
      { initialProps: { blocks: blocksA } },
    );

    // No save yet — first render's blocks match the "lastSaved" baseline.
    expect(calls).toHaveLength(0);
    expect(result.current.status).toBe('idle');

    // Edit the tree: this marks dirty + schedules the timer.
    rerender({ blocks: blocksB });
    // Before the timer fires, we're still idle.
    expect(calls).toHaveLength(0);

    // Advance past the interval; the timer should fire.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(calls).toHaveLength(1);
    const first = calls[0]!;
    expect(first.url).toContain('/api/v1/posts/post-1/autosave');
    expect(first.body).toEqual({ blocks: blocksB });
    await flush();
    expect(result.current.status).toBe('saved');
    expect(result.current.lastSavedAt).toBeInstanceOf(Date);
  });

  it('does NOT fire when blocks are unchanged', async () => {
    const { fn, calls } = makeFetchMock([{ status: 200, body: {} }]);
    renderHook(() =>
      useAutosave('post-1', blocksA, {
        intervalMs: 5000,
        debounceMs: 100,
        fetchImpl: fn,
      }),
    );
    // No change, no save — even after pushing the clock well past
    // the interval.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(calls).toHaveLength(0);
  });

  it('aborts an in-flight save on unmount', async () => {
    // Resolve the fetch on a deferred promise so we can observe the
    // abort signal mid-flight.
    let resolveFetch: ((r: Response) => void) | undefined;
    let receivedSignal: AbortSignal | undefined;
    const spy = vi.fn(async (_url: string, init?: RequestInit) => {
      receivedSignal = init?.signal ?? undefined;
      return new Promise<Response>((resolve) => {
        resolveFetch = resolve;
      });
    });
    const fn = spy as unknown as typeof fetch;
    const { rerender, unmount } = renderHook(
      ({ blocks }: { blocks: BlockTree }) =>
        useAutosave('post-1', blocks, {
          intervalMs: 1000,
          debounceMs: 50,
          fetchImpl: fn,
        }),
      { initialProps: { blocks: blocksA } },
    );
    rerender({ blocks: blocksB });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(spy).toHaveBeenCalledTimes(1);

    // Pull the editor: the abort signal must flip.
    unmount();
    expect(receivedSignal?.aborted).toBe(true);

    // Resolve the dangling fetch so vitest doesn't hang.
    resolveFetch?.({
      ok: true,
      status: 200,
      json: async () => ({}),
    } as unknown as Response);
  });

  it('surfaces 423 as an error', async () => {
    const { fn } = makeFetchMock([
      { status: 423, body: { code: 'locked' } },
    ]);
    const { result, rerender } = renderHook(
      ({ blocks }: { blocks: BlockTree }) =>
        useAutosave('post-1', blocks, {
          intervalMs: 1000,
          debounceMs: 50,
          fetchImpl: fn,
        }),
      { initialProps: { blocks: blocksA } },
    );
    rerender({ blocks: blocksB });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    await flush();
    expect(result.current.status).toBe('error');
    expect(result.current.error).toMatch(/locked/);
  });

  it('surfaces a generic 500 as an error', async () => {
    const { fn } = makeFetchMock([{ status: 500 }]);
    const { result, rerender } = renderHook(
      ({ blocks }: { blocks: BlockTree }) =>
        useAutosave('post-1', blocks, {
          intervalMs: 1000,
          debounceMs: 50,
          fetchImpl: fn,
        }),
      { initialProps: { blocks: blocksA } },
    );
    rerender({ blocks: blocksB });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    await flush();
    expect(result.current.status).toBe('error');
    expect(result.current.error).toMatch(/500/);
  });
});
