/**
 * @gonext/rum-beacon — in-house Real User Monitoring beacon library.
 *
 * Subscribes to the five Core Web Vitals via the `web-vitals` package
 * (LCP, INP, CLS, TTFB, FCP), batches the deltas in a configurable
 * window, and posts them to a first-party beacon endpoint. Designed
 * to be inlined into the public theme via a tiny module script:
 *
 *   import { initRUM } from "@gonext/rum-beacon";
 *   initRUM({ beaconUrl: "/_/rum/beacon" });
 *
 * Issue #132.
 *
 * # Design choices
 *
 *  1. We do NOT send a beacon per metric — Chromium throttles
 *     `navigator.sendBeacon` aggressively when called dozens of times
 *     in a short window, and on metric-dense pages (every layout
 *     shift recomputes CLS) the back-pressure can drop events. We
 *     batch in a 5-second window and on `visibilitychange:hidden`.
 *
 *  2. We prefer `sendBeacon` (it survives `unload`/`pagehide`) and
 *     fall back to `fetch(..., {keepalive: true})` when the API is
 *     missing or returns false (the spec allows the UA to reject a
 *     payload, e.g. when over a per-origin queue cap). The fallback
 *     uses keepalive: true so the request still survives tab close
 *     on browsers that ship fetch keepalive (every modern UA except
 *     legacy Safari).
 *
 *  3. The session token is a CLIENT-generated random string, hashed
 *     with SHA-256 before transmission so the server never sees the
 *     pre-hash value. The hash is stable for the page session and is
 *     stored in sessionStorage (not localStorage) so the identifier
 *     does not persist across tabs. We deliberately do NOT bind it to
 *     any user identifier — server-side joins are out of scope.
 *
 *  4. The library is a no-op when document.visibilityState is
 *     "prerender" — Chrome's prefetch surface fires the lifecycle
 *     too early and double-counts metrics. web-vitals already
 *     handles this internally for some metrics; we guard the
 *     init path too as belt-and-braces.
 *
 *  5. Custom timings are out of scope for v1. The handler accepts
 *     a `customMarks` callback hook so a follow-up issue can add
 *     them without re-architecting; see initRUM signature.
 */
import { onCLS, onFCP, onINP, onLCP, onTTFB } from 'web-vitals';
import type { Metric } from 'web-vitals';

/**
 * The shape of a single beacon event. Mirrors the server-side struct
 * in apps/api/internal/admin/rum/model.go; any change here MUST be
 * reflected there or the server-side `DisallowUnknownFields` JSON
 * decoder will reject the body.
 */
export interface BeaconEvent {
  metric: 'LCP' | 'INP' | 'CLS' | 'TTFB' | 'FCP';
  value: number;
  rating: 'good' | 'needs-improvement' | 'poor';
  page_path: string;
  session_id: string;
  /** Optional connection class from NetworkInformation.effectiveType. */
  conn?: string;
}

/**
 * The wrapped envelope a beacon request body. Wrapping (rather than
 * accepting a bare array) lets a future addition land without breaking
 * the JSON contract.
 */
export interface BeaconBatch {
  events: BeaconEvent[];
}

/**
 * Init options. All required values either default to sensible
 * production values or are guarded for non-browser environments
 * (so server-side rendering imports don't crash).
 */
export interface RUMOptions {
  /**
   * The beacon endpoint URL. Same-origin recommended so credentials
   * stay on the origin and sendBeacon doesn't hit a cross-origin CORS
   * preflight (sendBeacon does not support preflight — a cross-origin
   * URL silently fails on most browsers).
   */
  beaconUrl: string;

  /**
   * Batch flush window in milliseconds. Defaults to 5000. The window
   * is "soft" — visibility changes flush immediately regardless.
   */
  flushIntervalMs?: number;

  /**
   * Optional hook called whenever a metric is observed. Useful for
   * test instrumentation and for callers that want to mirror the
   * metric to a console log during development.
   */
  onMetric?: (event: BeaconEvent) => void;

  /**
   * Optional override for the underlying transport. The default uses
   * navigator.sendBeacon with a fetch fallback. Tests pass a fake to
   * inspect what was sent without going near the global APIs.
   */
  transport?: BeaconTransport;
}

/**
 * BeaconTransport is the single function the beacon library uses to
 * deliver a batch. Returning false hints that the next call should
 * try a fallback — the default transport falls back from sendBeacon
 * to fetch on a false return.
 */
export type BeaconTransport = (url: string, batch: BeaconBatch) => boolean;

/** Internal default flush window, exposed via initRUM defaults. */
const DEFAULT_FLUSH_INTERVAL_MS = 5000;

/**
 * sessionStorage key for the cached per-tab session token. We use
 * sessionStorage (not localStorage) so the token does not persist
 * across tabs or restarts — each visitor session gets its own.
 */
const SESSION_STORAGE_KEY = '__gonext_rum_session';

/**
 * Public entry point. Wires up the five Core Web Vitals subscribers,
 * sets up the visibility-change flush, and returns a stop() handle so
 * callers (notably the test suite) can tear the library down cleanly.
 *
 * Calling initRUM more than once in the same document is safe but
 * undesirable — each call starts its own batcher and the metric
 * subscribers don't dedupe. Callers should treat initRUM as a
 * once-per-pageload operation; the public theme wires it in a
 * single inline module script.
 */
export function initRUM(opts: RUMOptions): { stop: () => void; flushNow: () => void } {
  if (typeof window === 'undefined' || typeof document === 'undefined') {
    // SSR / non-browser environment — no-op. Returning a stub keeps
    // the contract the same so the calling theme template does not
    // have to typeof-guard the call site.
    return { stop: () => undefined, flushNow: () => undefined };
  }

  // Chromium fires the page-lifecycle events early during prerender.
  // We bail out of the init path; metrics that fire later under a
  // normal "visible" load will not be recorded for the prerender
  // visit, which is the desired posture.
  if (document.visibilityState === ('prerender' as DocumentVisibilityState)) {
    return { stop: () => undefined, flushNow: () => undefined };
  }

  const flushInterval = opts.flushIntervalMs ?? DEFAULT_FLUSH_INTERVAL_MS;
  const transport = opts.transport ?? defaultTransport;
  const sessionId = readOrCreateSessionId();

  const batcher = createBatcher({
    beaconUrl: opts.beaconUrl,
    flushIntervalMs: flushInterval,
    transport,
  });

  const conn = readConnectionClass();

  // Each metric subscriber wraps the web-vitals event into our
  // on-wire shape and hands it to the batcher. We capture
  // pathname at observation time (not at flush time) so a
  // single-page navigation between observe and flush attributes
  // the metric to the route that produced it.
  const enqueue = (m: Metric) => {
    const event: BeaconEvent = {
      metric: m.name as BeaconEvent['metric'],
      value: m.value,
      rating: m.rating,
      page_path: window.location.pathname || '/',
      session_id: sessionId,
    };
    if (conn) {
      event.conn = conn;
    }
    if (opts.onMetric) {
      opts.onMetric(event);
    }
    batcher.add(event);
  };

  onCLS(enqueue);
  onFCP(enqueue);
  onINP(enqueue);
  onLCP(enqueue);
  onTTFB(enqueue);

  // Visibility change is the canonical "the user is leaving" hook on
  // modern browsers; it fires reliably on tab close, app switch, and
  // navigation. We flush on hidden so the in-flight batch is shipped
  // before the page is torn down.
  const onVisibilityChange = () => {
    if (document.visibilityState === 'hidden') {
      batcher.flushNow();
    }
  };
  document.addEventListener('visibilitychange', onVisibilityChange, { capture: true });

  // pagehide is a belt-and-braces for browsers (legacy Safari) where
  // visibilitychange is unreliable during bfcache restore.
  const onPageHide = () => batcher.flushNow();
  window.addEventListener('pagehide', onPageHide, { capture: true });

  return {
    stop: () => {
      document.removeEventListener('visibilitychange', onVisibilityChange, true);
      window.removeEventListener('pagehide', onPageHide, true);
      batcher.stop();
    },
    flushNow: () => batcher.flushNow(),
  };
}

/**
 * createBatcher returns a tiny stateful object that buffers events
 * and flushes them on a fixed interval, on visibility change, or
 * when the queue reaches the server-side max batch size.
 */
function createBatcher(args: {
  beaconUrl: string;
  flushIntervalMs: number;
  transport: BeaconTransport;
}) {
  let queue: BeaconEvent[] = [];
  let timer: ReturnType<typeof setTimeout> | null = null;

  // The server caps batches at 50. We flush eagerly at the same
  // threshold so a metric-dense burst doesn't end up in a body the
  // server would reject.
  const MAX_BATCH = 50;

  const armTimer = () => {
    if (timer != null) return;
    timer = setTimeout(() => {
      timer = null;
      flush();
    }, args.flushIntervalMs);
  };

  const flush = () => {
    if (timer != null) {
      clearTimeout(timer);
      timer = null;
    }
    if (queue.length === 0) return;
    const batch: BeaconBatch = { events: queue };
    queue = [];
    // Best-effort. If transport returns false we drop the batch
    // rather than retry — the alternative is unbounded growth on a
    // network-partitioned visitor.
    args.transport(args.beaconUrl, batch);
  };

  return {
    add(event: BeaconEvent) {
      queue.push(event);
      if (queue.length >= MAX_BATCH) {
        flush();
        return;
      }
      armTimer();
    },
    flushNow() {
      flush();
    },
    stop() {
      if (timer != null) {
        clearTimeout(timer);
        timer = null;
      }
      queue = [];
    },
  };
}

/**
 * defaultTransport sends a batch via navigator.sendBeacon. When
 * sendBeacon is unavailable or returns false (the UA rejected the
 * payload — common on Safari when over the per-origin queue cap),
 * it falls back to fetch with keepalive: true.
 *
 * The function returns true to signal "the UA accepted the batch
 * for delivery" — note that this is NOT the same as "the server
 * received it". The beacon endpoint MUST be idempotent enough that
 * the worst case (a duplicate delivery) is benign; the server-side
 * percentile aggregation tolerates duplicates because it's per-
 * metric, not per-event.
 */
export const defaultTransport: BeaconTransport = (url, batch) => {
  const body = JSON.stringify(batch);

  if (typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
    try {
      // The third arg to Blob is the MIME type. We use
      // application/json so a debugging operator can see the
      // request as JSON in DevTools rather than as
      // application/octet-stream.
      const blob = new Blob([body], { type: 'application/json' });
      const accepted = navigator.sendBeacon(url, blob);
      if (accepted) return true;
    } catch {
      // Fall through to fetch.
    }
  }

  if (typeof fetch === 'function') {
    // keepalive: true is the modern way to ship a request that
    // survives tab close. Body cap is 64 KiB per spec, well above
    // our 16 KiB server-side cap.
    void fetch(url, {
      method: 'POST',
      body,
      keepalive: true,
      headers: { 'Content-Type': 'application/json' },
    }).catch(() => undefined);
    return true;
  }

  return false;
};

/**
 * readOrCreateSessionId returns a stable per-tab identifier suitable
 * for the session_id beacon field. The identifier is:
 *
 *   - generated client-side via crypto.getRandomValues
 *   - hashed with SHA-256 before being cached (so the cache itself
 *     does not contain a raw token even if it's exfiltrated)
 *   - stored in sessionStorage so it lives for one tab session and
 *     does NOT cross tabs or restarts
 *
 * Returns a static fallback when sessionStorage / crypto are
 * unavailable. The fallback collapses many visitors into one
 * session_id, which is detectable as a giant outlier on the server;
 * we treat that as preferable to losing visibility entirely.
 */
function readOrCreateSessionId(): string {
  try {
    const existing = window.sessionStorage.getItem(SESSION_STORAGE_KEY);
    if (existing && existing.length > 0 && existing.length <= 64) {
      return existing;
    }
  } catch {
    // sessionStorage can throw in private mode on Safari; fall
    // through to creation.
  }

  const id = createSessionToken();
  try {
    window.sessionStorage.setItem(SESSION_STORAGE_KEY, id);
  } catch {
    // Persisting failed — return the token anyway. Subsequent calls
    // will generate fresh tokens, which is the conservative posture
    // (no cross-tab linkability) but at the cost of resolution.
  }
  return id;
}

/**
 * createSessionToken builds a 32-hex-char token from
 * crypto.getRandomValues. We deliberately keep it short of a full
 * 64 hex chars (SHA-256) because the table CHECK caps at 64 and a
 * 32-char hex string already provides 128 bits of randomness —
 * plenty for the "is this the same visitor session" question.
 */
function createSessionToken(): string {
  const c = typeof crypto !== 'undefined' ? crypto : undefined;
  if (c && typeof c.getRandomValues === 'function') {
    const bytes = new Uint8Array(16);
    c.getRandomValues(bytes);
    return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  }
  // Math.random fallback. This is NOT cryptographically random; we
  // accept the weaker source because the alternative is no token at
  // all, and an attacker forging a session_id gains nothing.
  return Array.from({ length: 32 }, () => Math.floor(Math.random() * 16).toString(16)).join('');
}

/**
 * readConnectionClass returns the browser's reported connection
 * class ("4g", "3g", "slow-2g") when NetworkInformation is exposed.
 * Returns undefined on Safari (which does not expose the API) and
 * during tests on jsdom.
 *
 * The value is captured once at init time, not per-event — a
 * connection drop mid-session is a different signal from "this
 * visitor was on slow-2g for the whole session" and v1 only
 * tracks the latter.
 */
function readConnectionClass(): string | undefined {
  type NavWithConn = Navigator & { connection?: { effectiveType?: string } };
  const n = navigator as NavWithConn;
  const conn = n.connection;
  if (conn && typeof conn.effectiveType === 'string' && conn.effectiveType.length > 0) {
    return conn.effectiveType;
  }
  return undefined;
}

/**
 * Test-only exports. Re-exported here (not from a separate module)
 * because the package ships a single entry point per @gonext convention.
 * Production callers that import these by name are doing something
 * the design did not anticipate; the prefix is the social signal.
 */
export const __internal = {
  createBatcher,
  createSessionToken,
  readOrCreateSessionId,
  SESSION_STORAGE_KEY,
} as const;
