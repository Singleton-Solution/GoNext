/**
 * Tests for `usePostLock`.
 *
 * We cover the three states:
 *
 *  - Acquire on mount; reports `locked: true` once the server confirms.
 *  - Other-user-holds reports `lockedBy` with the holder shape.
 *  - Unmount issues a DELETE if we held the lock.
 *
 * The hook owns timers; we use Vitest's fake-timer mode so heartbeat
 * tests don't need to wait on the wall clock.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { usePostLock } from './usePostLock.ts';

beforeEach(() => {
  // shouldAdvanceTime: see note in useAutosave.test.tsx — keeps the
  // microtask queue draining so awaited fetch-mock promises settle
  // without manual prodding.
  vi.useFakeTimers({ shouldAdvanceTime: true });
});
afterEach(() => {
  vi.useRealTimers();
});

async function flush(): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}

interface FetchCall {
  url: string;
  method: string;
}

/**
 * Build a fetch mock keyed by HTTP method: the lock acquire POST and
 * the release DELETE need distinct responses, and the test wants to
 * inspect each call. The mock records every invocation and consults
 * `responses` in order for each call.
 */
function makeFetchMock(responses: Array<{ status: number; body?: unknown }>): {
  fn: typeof fetch;
  calls: FetchCall[];
} {
  const calls: FetchCall[] = [];
  let i = 0;
  const fn = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method ?? 'GET' });
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

describe('usePostLock', () => {
  it('acquires on mount and reports locked: true', async () => {
    const { fn, calls } = makeFetchMock([{ status: 200, body: {} }]);
    const { result } = renderHook(() =>
      usePostLock('post-1', {
        heartbeatMs: 60_000,
        fetchImpl: fn,
      }),
    );
    await flush();
    expect(result.current.locked).toBe(true);
    expect(result.current.lockedBy).toBeNull();
    const first = calls[0]!;
    expect(first.url).toContain('/api/v1/posts/post-1/lock');
    expect(first.method).toBe('POST');
  });

  it('reports lockedBy when another user holds the lock', async () => {
    const { fn } = makeFetchMock([
      {
        status: 200,
        body: {
          holder: {
            user_id: 'u-other',
            display_name: 'Alice',
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          },
        },
      },
    ]);
    const { result } = renderHook(() =>
      usePostLock('post-1', {
        heartbeatMs: 60_000,
        fetchImpl: fn,
      }),
    );
    await flush();
    expect(result.current.locked).toBe(false);
    expect(result.current.lockedBy).not.toBeNull();
    expect(result.current.lockedBy?.userId).toBe('u-other');
    expect(result.current.lockedBy?.displayName).toBe('Alice');
  });

  it('heartbeats every heartbeatMs', async () => {
    const { fn, calls } = makeFetchMock([
      { status: 200, body: {} },
      { status: 200, body: {} },
      { status: 200, body: {} },
    ]);
    renderHook(() =>
      usePostLock('post-1', {
        heartbeatMs: 1000,
        fetchImpl: fn,
      }),
    );
    // Initial acquire.
    await flush();
    const initial = calls.length;
    expect(initial).toBeGreaterThanOrEqual(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2500);
    });
    // Should have ticked at least twice more (1000ms, 2000ms).
    expect(calls.length).toBeGreaterThanOrEqual(initial + 2);
    expect(calls.every((c) => c.method === 'POST')).toBe(true);
  });

  it('releases (DELETE) on unmount if we held it', async () => {
    const { fn, calls } = makeFetchMock([
      { status: 200, body: {} }, // acquire
      { status: 204 }, // release
    ]);
    const { unmount, result } = renderHook(() =>
      usePostLock('post-1', {
        heartbeatMs: 60_000,
        fetchImpl: fn,
      }),
    );
    await flush();
    expect(result.current.locked).toBe(true);
    unmount();
    await flush();
    const deletes = calls.filter((c) => c.method === 'DELETE');
    expect(deletes.length).toBeGreaterThanOrEqual(1);
  });

  it('does NOT release on unmount if another user held it', async () => {
    const { fn, calls } = makeFetchMock([
      {
        status: 200,
        body: {
          holder: {
            user_id: 'u-other',
            display_name: 'Alice',
            expires_at: new Date(Date.now() + 60_000).toISOString(),
          },
        },
      },
    ]);
    const { unmount, result } = renderHook(() =>
      usePostLock('post-1', {
        heartbeatMs: 60_000,
        fetchImpl: fn,
      }),
    );
    await flush();
    expect(result.current.lockedBy).not.toBeNull();
    unmount();
    await flush();
    const deletes = calls.filter((c) => c.method === 'DELETE');
    expect(deletes).toHaveLength(0);
  });

  it('refreshLock triggers a fresh POST', async () => {
    const { fn, calls } = makeFetchMock([
      { status: 200, body: {} },
      { status: 200, body: {} },
    ]);
    const { result } = renderHook(() =>
      usePostLock('post-1', {
        heartbeatMs: 60_000,
        fetchImpl: fn,
      }),
    );
    await flush();
    expect(result.current.locked).toBe(true);
    const before = calls.length;
    await act(async () => {
      await result.current.refreshLock();
    });
    expect(calls.length).toBeGreaterThan(before);
  });
});
