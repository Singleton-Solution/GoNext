/**
 * Vitest suite for @gonext/rum-beacon.
 *
 * The tests work against the jsdom environment provided by the
 * shared base config. We stub navigator.sendBeacon / global.fetch
 * per-test rather than wiring a single setup file, so each test is
 * self-contained and the diff between "what changed" and "what
 * remained the same" is obvious to a reviewer.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  initRUM,
  defaultTransport,
  __internal,
  type BeaconBatch,
  type BeaconEvent,
} from './index';

// Stub the web-vitals module so the test can drive metric callbacks
// deterministically. Without this, the real `web-vitals` library
// reaches for performance.timing / PerformanceObserver — under jsdom
// those exist but never fire, so the subscribers would be silent.
vi.mock('web-vitals', () => {
  const calls = {
    onCLS: vi.fn(),
    onFCP: vi.fn(),
    onINP: vi.fn(),
    onLCP: vi.fn(),
    onTTFB: vi.fn(),
  };
  return calls;
});

// Re-import the mocked subscriber stubs so a test can invoke them
// directly and simulate the browser firing a metric.
import * as webVitals from 'web-vitals';

beforeEach(() => {
  // Clear sessionStorage so every test starts with a fresh ID.
  window.sessionStorage.clear();
  // Reset all mocks so call counts don't leak between tests.
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('initRUM', () => {
  it('subscribes to all five Core Web Vitals', () => {
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    initRUM({ beaconUrl: '/_/rum/beacon', transport });
    expect(webVitals.onCLS).toHaveBeenCalledTimes(1);
    expect(webVitals.onFCP).toHaveBeenCalledTimes(1);
    expect(webVitals.onINP).toHaveBeenCalledTimes(1);
    expect(webVitals.onLCP).toHaveBeenCalledTimes(1);
    expect(webVitals.onTTFB).toHaveBeenCalledTimes(1);
  });

  it('flushes a beacon after the configured interval', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    initRUM({ beaconUrl: '/_/rum/beacon', flushIntervalMs: 1000, transport });

    // Drive a single metric.
    const cb = vi.mocked(webVitals.onLCP).mock.calls[0]?.[0];
    expect(cb).toBeDefined();
    cb!({
      name: 'LCP',
      value: 2400,
      rating: 'needs-improvement',
      delta: 2400,
      id: 'a',
      navigationType: 'navigate',
      entries: [],
    });

    expect(transport).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1000);
    expect(transport).toHaveBeenCalledTimes(1);

    const [url, batch] = transport.mock.calls[0]!;
    expect(url).toBe('/_/rum/beacon');
    expect((batch as BeaconBatch).events).toHaveLength(1);
    expect((batch as BeaconBatch).events[0]!.metric).toBe('LCP');
  });

  it('flushes immediately on visibilitychange:hidden', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    initRUM({ beaconUrl: '/_/rum/beacon', flushIntervalMs: 60_000, transport });

    const cb = vi.mocked(webVitals.onCLS).mock.calls[0]?.[0];
    cb!({
      name: 'CLS',
      value: 0.05,
      rating: 'good',
      delta: 0.05,
      id: 'b',
      navigationType: 'navigate',
      entries: [],
    });

    // Switch the page to hidden.
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => 'hidden',
    });
    document.dispatchEvent(new Event('visibilitychange'));

    expect(transport).toHaveBeenCalledTimes(1);
  });

  it('flushes eagerly when the queue reaches the 50-event cap', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    initRUM({ beaconUrl: '/_/rum/beacon', flushIntervalMs: 60_000, transport });

    const cb = vi.mocked(webVitals.onINP).mock.calls[0]?.[0];
    for (let i = 0; i < 50; i++) {
      cb!({
        name: 'INP',
        value: 200 + i,
        rating: 'good',
        delta: 200 + i,
        id: `n-${i}`,
        navigationType: 'navigate',
        entries: [],
      });
    }

    expect(transport).toHaveBeenCalledTimes(1);
    const [, batch] = transport.mock.calls[0]!;
    expect((batch as BeaconBatch).events).toHaveLength(50);
  });

  it('respects an onMetric hook', () => {
    const onMetric = vi.fn<[BeaconEvent], void>();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    initRUM({ beaconUrl: '/_/rum/beacon', transport, onMetric });

    const cb = vi.mocked(webVitals.onFCP).mock.calls[0]?.[0];
    cb!({
      name: 'FCP',
      value: 1200,
      rating: 'good',
      delta: 1200,
      id: 'c',
      navigationType: 'navigate',
      entries: [],
    });

    expect(onMetric).toHaveBeenCalledTimes(1);
    expect(onMetric.mock.calls[0]?.[0].metric).toBe('FCP');
    expect(onMetric.mock.calls[0]?.[0].rating).toBe('good');
  });

  it('reuses the cached session_id across metrics', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    initRUM({ beaconUrl: '/_/rum/beacon', flushIntervalMs: 1000, transport });

    const fcp = vi.mocked(webVitals.onFCP).mock.calls[0]?.[0];
    const lcp = vi.mocked(webVitals.onLCP).mock.calls[0]?.[0];
    fcp!({
      name: 'FCP',
      value: 800,
      rating: 'good',
      delta: 800,
      id: 'x',
      navigationType: 'navigate',
      entries: [],
    });
    lcp!({
      name: 'LCP',
      value: 2200,
      rating: 'needs-improvement',
      delta: 2200,
      id: 'y',
      navigationType: 'navigate',
      entries: [],
    });

    vi.advanceTimersByTime(1000);
    const [, batch] = transport.mock.calls[0]!;
    const events = (batch as BeaconBatch).events;
    expect(events).toHaveLength(2);
    expect(events[0]!.session_id).toBeDefined();
    expect(events[0]!.session_id).toEqual(events[1]!.session_id);
  });

  it('stop() tears down listeners and pending timers', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    const handle = initRUM({ beaconUrl: '/_/rum/beacon', flushIntervalMs: 1000, transport });

    const cb = vi.mocked(webVitals.onLCP).mock.calls[0]?.[0];
    cb!({
      name: 'LCP',
      value: 1500,
      rating: 'good',
      delta: 1500,
      id: 'z',
      navigationType: 'navigate',
      entries: [],
    });

    handle.stop();
    vi.advanceTimersByTime(5000);
    expect(transport).not.toHaveBeenCalled();
  });
});

describe('defaultTransport', () => {
  it('uses navigator.sendBeacon when available and returns true', () => {
    const sendBeacon = vi.fn().mockReturnValue(true);
    Object.defineProperty(navigator, 'sendBeacon', {
      configurable: true,
      value: sendBeacon,
    });

    const ok = defaultTransport('/_/rum/beacon', { events: [sampleEvent()] });
    expect(ok).toBe(true);
    expect(sendBeacon).toHaveBeenCalledTimes(1);
    expect(sendBeacon.mock.calls[0][0]).toBe('/_/rum/beacon');
  });

  it('falls back to fetch when sendBeacon returns false', () => {
    const sendBeacon = vi.fn().mockReturnValue(false);
    Object.defineProperty(navigator, 'sendBeacon', {
      configurable: true,
      value: sendBeacon,
    });
    const fetchStub = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    globalThis.fetch = fetchStub as unknown as typeof fetch;

    const ok = defaultTransport('/_/rum/beacon', { events: [sampleEvent()] });
    expect(ok).toBe(true);
    expect(sendBeacon).toHaveBeenCalledTimes(1);
    expect(fetchStub).toHaveBeenCalledTimes(1);
    const init = fetchStub.mock.calls[0][1];
    expect(init.method).toBe('POST');
    expect(init.keepalive).toBe(true);
  });

  it('falls back to fetch when sendBeacon throws', () => {
    Object.defineProperty(navigator, 'sendBeacon', {
      configurable: true,
      value: () => {
        throw new Error('boom');
      },
    });
    const fetchStub = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    globalThis.fetch = fetchStub as unknown as typeof fetch;

    const ok = defaultTransport('/_/rum/beacon', { events: [sampleEvent()] });
    expect(ok).toBe(true);
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });

  it('uses fetch when sendBeacon is undefined', () => {
    Object.defineProperty(navigator, 'sendBeacon', {
      configurable: true,
      value: undefined,
    });
    const fetchStub = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    globalThis.fetch = fetchStub as unknown as typeof fetch;

    const ok = defaultTransport('/_/rum/beacon', { events: [sampleEvent()] });
    expect(ok).toBe(true);
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });
});

describe('createBatcher (internal)', () => {
  it('debounces under the flush interval', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    const b = __internal.createBatcher({
      beaconUrl: '/_/rum/beacon',
      flushIntervalMs: 1000,
      transport,
    });
    b.add(sampleEvent());
    b.add(sampleEvent());
    vi.advanceTimersByTime(500);
    expect(transport).not.toHaveBeenCalled();
    vi.advanceTimersByTime(500);
    expect(transport).toHaveBeenCalledTimes(1);
    expect((transport.mock.calls[0]![1] as BeaconBatch).events).toHaveLength(2);
  });

  it('flushNow flushes the current queue immediately', () => {
    vi.useFakeTimers();
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    const b = __internal.createBatcher({
      beaconUrl: '/_/rum/beacon',
      flushIntervalMs: 60_000,
      transport,
    });
    b.add(sampleEvent());
    b.flushNow();
    expect(transport).toHaveBeenCalledTimes(1);
  });

  it('does not flush on an empty queue', () => {
    const transport = vi.fn<[string, BeaconBatch], boolean>().mockReturnValue(true);
    const b = __internal.createBatcher({
      beaconUrl: '/_/rum/beacon',
      flushIntervalMs: 1000,
      transport,
    });
    b.flushNow();
    expect(transport).not.toHaveBeenCalled();
  });
});

describe('session token', () => {
  it('round-trips a token through sessionStorage', () => {
    const first = __internal.readOrCreateSessionId();
    const second = __internal.readOrCreateSessionId();
    expect(first).toBe(second);
    expect(window.sessionStorage.getItem(__internal.SESSION_STORAGE_KEY)).toBe(first);
  });

  it('creates a 32-char hex token by default', () => {
    const id = __internal.createSessionToken();
    expect(id).toMatch(/^[0-9a-f]{32}$/);
  });
});

// Reusable beacon event fixture. Keeps the test bodies focused on
// the assertion under test, not on shaping plausible data.
function sampleEvent(): BeaconEvent {
  return {
    metric: 'LCP',
    value: 2500,
    rating: 'needs-improvement',
    page_path: '/',
    session_id: 'abc',
  };
}
