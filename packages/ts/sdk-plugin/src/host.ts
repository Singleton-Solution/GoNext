/**
 * Typed wrappers around the host's `gn_*` ABI imports.
 *
 * Javy exposes the host imports as plain JavaScript globals on
 * `globalThis`. From the plugin author's point of view, the host
 * surface is a function table:
 *
 *     globalThis.gn_log(level, ptr, len)
 *     globalThis.gn_kv_set(key, value)
 *     globalThis.gn_http_fetch(envelope)
 *     ...
 *
 * Each Javy WASI shim handles pointer / length translation under the
 * hood — when the plugin calls a typed wrapper such as `host.kv.set("k",
 * "v")`, the underlying host function receives a marshaled JSON
 * envelope (or a plain string), runs the host-side capability +
 * audit logic, and either succeeds or returns a typed failure status.
 *
 * This module mirrors the host's `gn_*` Go surface 1:1. Anything not
 * exposed here would force the plugin to reach into `globalThis`
 * directly, which works but loses the type-checking sweet spot Javy
 * gives us.
 *
 * Why the indirection: Javy's binding generator does not expose typed
 * stubs out of the box. Without this module each plugin would have to
 * declare `gn_kv_get`'s shape inline. By centralising the declarations
 * here we (a) get one TypeScript source of truth, (b) can swap the
 * wire format (JSON, msgpack) without touching plugin code, and (c)
 * keep the wrapper logic — for example, throwing on a negative-length
 * return — in one place.
 */

import {
  ResultStatus,
  type ResultStatusCode,
} from './codec.ts';

/**
 * Shape of the host bindings as Javy exposes them. The runtime is
 * responsible for filling `globalThis.gn_*` with these functions
 * before invoking the guest's `gn_handle_hook` entry point. Tests can
 * shim the same functions on `globalThis` to exercise the wrapper
 * surface without standing up a real wazero host.
 *
 * Pointer / length pairs in the raw Go ABI become ordinary strings or
 * bytes here — Javy's runtime translates them. Where the host returns
 * a packed `i64`, Javy surfaces the high half (pointer-resolved string)
 * and the low half (status) as a structured object so the wrapper can
 * branch on the sentinel without bit-twiddling.
 *
 * NOTE: The exact bridging convention is set by Javy. The shape below
 * is the one this SDK contracts against; the build CLI (see
 * `bin/gonext-sdk-build.js`) installs a thin Javy host adapter that
 * exposes these functions under the names below.
 */
export interface HostBindings {
  // ─────────────────────────── env ───────────────────────────
  gn_log?: (level: number, message: string) => void;
  gn_time_ms?: () => number;
  gn_panic?: (message: string) => void;

  // ─────────────────────── observability ────────────────────
  gn_i18n_translate?: (key: string, locale: string) => string | null;
  gn_metric_observe?: (
    name: string,
    value: number,
    tags?: Record<string, string>,
  ) => number;
  gn_event_emit?: (name: string, data?: Record<string, unknown>) => number;
  gn_span_event?: (
    name: string,
    attrs?: Record<string, string>,
  ) => number;

  // ─────────────────────────── data ──────────────────────────
  gn_db_read?: (
    query: string,
    args?: readonly unknown[],
  ) => HostDataResult;
  gn_db_write?: (
    query: string,
    args?: readonly unknown[],
  ) => HostDataResult;
  gn_kv_get?: (key: string) => HostDataResult;
  gn_kv_set?: (key: string, value: string) => HostDataResult;
  gn_kv_del?: (key: string) => HostDataResult;
  gn_kv_incr?: (key: string, delta: number) => HostDataResult;
  gn_cache_invalidate?: (tags: readonly string[]) => HostDataResult;

  // ────────────────────────── network ────────────────────────
  gn_http_fetch?: (envelope: HttpFetchRequest) => HostDataResult;
  gn_media_read?: (id: string) => HostDataResult;
  gn_users_read?: (id: string) => HostDataResult;

  // ─────────────────────────── platform ───────────────────────
  gn_secrets_get?: (name: string) => HostDataResult;
  gn_audit_emit?: (
    event: string,
    metadata?: Record<string, unknown>,
  ) => number;
  gn_cron_register?: (
    spec: string,
    job: string,
  ) => number;
}

/**
 * Tagged result the Javy adapter returns for data-shaped host calls.
 *
 * On success `status === 0` (`ResultStatus.OK`) and either `value`
 * carries the raw response body (string) or it's empty for delete-like
 * operations. On failure `status` is one of the negative-int32
 * sentinels — see `packages/go/plugins/runtime/host_data.go` for the
 * canonical list of values.
 */
export interface HostDataResult {
  status: number;
  value?: string;
}

/** Bridging map for HTTP log levels. Same numerics the host expects. */
export const LogLevel = {
  Debug: 0,
  Info: 1,
  Warn: 2,
  Error: 3,
} as const;

/** Type-level enumeration of log levels. */
export type LogLevelCode = (typeof LogLevel)[keyof typeof LogLevel];

/** Outbound HTTP-fetch envelope — matches `httpFetchRequest` in Go. */
export interface HttpFetchRequest {
  method?: string;
  url: string;
  headers?: Record<string, string>;
  body?: string;
}

/** Decoded HTTP-fetch response. Mirrors the Go side's envelope. */
export interface HttpFetchResponse {
  status: number;
  headers?: Record<string, string>;
  body?: string;
  error?: string;
}

/**
 * Typed failure thrown from any host wrapper that maps a host sentinel
 * to an error. Plugin code can `try/catch` to recover, or let it bubble
 * — the runtime entry point in `index.ts` catches it and translates
 * into the right packed return for the host.
 */
export class HostError extends Error {
  override readonly name = 'HostError';
  readonly status: ResultStatusCode | number;
  readonly call: string;
  constructor(call: string, status: number, message?: string) {
    super(message ?? `host call ${call} failed with status ${status}`);
    this.call = call;
    this.status = status;
  }
}

/** Resolve the live host bindings from globalThis. */
function bindings(): HostBindings {
  return globalThis as unknown as HostBindings;
}

function requireBinding<K extends keyof HostBindings>(
  name: K,
): NonNullable<HostBindings[K]> {
  const fn = bindings()[name];
  if (!fn) {
    throw new HostError(
      String(name),
      ResultStatus.Error,
      `host binding ${String(name)} is not available`,
    );
  }
  return fn as NonNullable<HostBindings[K]>;
}

function expectOk(call: string, result: HostDataResult): HostDataResult {
  if (result.status !== ResultStatus.OK) {
    throw new HostError(call, result.status);
  }
  return result;
}

// ────────────────────────────────────────────────────────────────────
// Public host surface, namespaced by capability domain.
// ────────────────────────────────────────────────────────────────────

/**
 * Structured logger wrapping `gn_log`. Each level produces a
 * host-side `slog` line attributed to the plugin's slug.
 *
 * Calls are best-effort: a misbehaving log line cannot fail a hook.
 * If the host binding is missing (test environment), the call is a
 * no-op so plugins can be unit-tested without standing up a Javy
 * shim.
 */
export const log = {
  debug(message: string): void {
    bindings().gn_log?.(LogLevel.Debug, message);
  },
  info(message: string): void {
    bindings().gn_log?.(LogLevel.Info, message);
  },
  warn(message: string): void {
    bindings().gn_log?.(LogLevel.Warn, message);
  },
  error(message: string): void {
    bindings().gn_log?.(LogLevel.Error, message);
  },
} as const;

/**
 * Wall-clock time in milliseconds. Wraps `gn_time_ms`. The host's
 * source is `time.Now().UnixMilli()` — not strictly monotonic.
 *
 * Returns `Date.now()` as a fallback when the binding is missing, so
 * unit tests don't have to mock it.
 */
export function nowMs(): number {
  return bindings().gn_time_ms?.() ?? Date.now();
}

/**
 * KV namespace bound to the plugin's slug. Wraps `gn_kv_*`.
 *
 * All methods may throw {@link HostError} with one of the data
 * sentinels (denied, internal, bad_args, not_found, quota). Plugins
 * that want fall-through reads should `try/catch` on
 * `kv.get` — a missing key surfaces as `status === -4` (NotFound).
 */
export const kv = {
  /**
   * Read a key. Returns the stored string or `null` when the key is
   * missing. Throws {@link HostError} for any other failure.
   */
  get(key: string): string | null {
    const fn = requireBinding('gn_kv_get');
    const res = fn(key);
    if (res.status === ResultStatus.OK) return res.value ?? '';
    if (res.status === -4 /* dataResultNotFound */) return null;
    throw new HostError('gn_kv_get', res.status);
  },
  /**
   * Write a key. `value` is taken as a string — callers wanting to
   * persist structured data must JSON-encode it first. Audited.
   */
  set(key: string, value: string): void {
    const fn = requireBinding('gn_kv_set');
    expectOk('gn_kv_set', fn(key, value));
  },
  /** Delete a key. Idempotent (deleting a missing key is a no-op). */
  del(key: string): void {
    const fn = requireBinding('gn_kv_del');
    expectOk('gn_kv_del', fn(key));
  },
  /**
   * Atomically increment a counter by `delta`. Returns the new value.
   * Counters are subject to the per-plugin key quota but not the byte
   * quota.
   */
  incr(key: string, delta = 1): number {
    const fn = requireBinding('gn_kv_incr');
    const res = fn(key, delta);
    if (res.status === ResultStatus.OK) {
      return res.value ? Number(res.value) : 0;
    }
    // The host packs the new value into the low 32 bits when it's
    // non-negative; Javy surfaces it as the `status` field. Anything
    // negative is a sentinel.
    if (res.status >= 0) return res.status;
    throw new HostError('gn_kv_incr', res.status);
  },
} as const;

/**
 * DB-ABI wrappers. The host runs every query under the plugin's
 * SET LOCAL ROLE and the manifest's per-plugin allowlist, so plugins
 * see exactly the GRANTs the operator configured.
 */
export const db = {
  /**
   * Execute a parameterised SELECT/WITH. Returns the decoded JSON
   * rowset. Throws on denied / bad_args / internal.
   */
  read<T = unknown>(query: string, args?: readonly unknown[]): T[] {
    const fn = requireBinding('gn_db_read');
    const res = expectOk('gn_db_read', fn(query, args));
    if (!res.value) return [];
    return JSON.parse(res.value) as T[];
  },
  /**
   * Execute a parameterised INSERT/UPDATE/DELETE. Returns the number
   * of affected rows. Audited.
   */
  write(query: string, args?: readonly unknown[]): number {
    const fn = requireBinding('gn_db_write');
    const res = fn(query, args);
    if (res.status >= 0) return res.status;
    throw new HostError('gn_db_write', res.status);
  },
} as const;

/** Cache invalidation. Tags are persisted to the outbox table. */
export const cache = {
  invalidate(tags: readonly string[]): void {
    const fn = requireBinding('gn_cache_invalidate');
    expectOk('gn_cache_invalidate', fn(tags));
  },
} as const;

/**
 * HTTP / media / users — read-side network host bindings. The host
 * enforces the per-plugin allowlist + SSRF guard + rate limiter
 * before any call leaves the box.
 */
export const http = {
  /**
   * Issue an outbound HTTP request via `gn_http_fetch`. The host
   * caps redirects (3), body size (10 MiB), and timeout (30s); any
   * value the plugin sets in the envelope is a request, not a
   * guarantee.
   */
  fetch(envelope: HttpFetchRequest): HttpFetchResponse {
    const fn = requireBinding('gn_http_fetch');
    const res = fn(envelope);
    if (res.status === ResultStatus.OK && res.value) {
      return JSON.parse(res.value) as HttpFetchResponse;
    }
    throw new HostError('gn_http_fetch', res.status);
  },
} as const;

/** Media read-only lookup, gated by `media.read`. */
export const media = {
  read<T = unknown>(id: string): T {
    const fn = requireBinding('gn_media_read');
    const res = fn(id);
    if (res.status === ResultStatus.OK && res.value) {
      return JSON.parse(res.value) as T;
    }
    throw new HostError('gn_media_read', res.status);
  },
} as const;

/** Users read-only lookup, gated by `users.read`. */
export const users = {
  read<T = unknown>(id: string): T {
    const fn = requireBinding('gn_users_read');
    const res = fn(id);
    if (res.status === ResultStatus.OK && res.value) {
      return JSON.parse(res.value) as T;
    }
    throw new HostError('gn_users_read', res.status);
  },
} as const;

/**
 * Audit-log emission. The host writes the row attributed to the
 * plugin's slug. Use this for plugin-defined administrative
 * actions; data-ABI writes are audited automatically.
 */
export const audit = {
  emit(event: string, metadata?: Record<string, unknown>): void {
    const fn = bindings().gn_audit_emit;
    if (!fn) return; // best-effort — missing binding is a no-op
    fn(event, metadata);
  },
} as const;

/**
 * Secrets read. The host materialises the secret from its configured
 * provider (env, file, KMS) and returns the value as a string. Returns
 * `null` if the secret is not configured.
 */
export const secrets = {
  get(name: string): string | null {
    const fn = bindings().gn_secrets_get;
    if (!fn) return null;
    const res = fn(name);
    if (res.status === ResultStatus.OK) return res.value ?? '';
    if (res.status === -4) return null;
    throw new HostError('gn_secrets_get', res.status);
  },
} as const;

/** Cron registration. Plugins declare jobs in the manifest; this is the runtime hook. */
export const cron = {
  register(spec: string, job: string): void {
    const fn = bindings().gn_cron_register;
    if (!fn) return;
    fn(spec, job);
  },
} as const;

/** Localised string lookup wrapping `gn_i18n_translate`. */
export const i18n = {
  translate(key: string, locale: string): string {
    const translated = bindings().gn_i18n_translate?.(key, locale);
    return translated ?? key;
  },
} as const;

/** Metric observation and span events. */
export const observe = {
  metric(name: string, value: number, tags?: Record<string, string>): void {
    bindings().gn_metric_observe?.(name, value, tags);
  },
  event(name: string, data?: Record<string, unknown>): void {
    bindings().gn_event_emit?.(name, data);
  },
  spanEvent(name: string, attrs?: Record<string, string>): void {
    bindings().gn_span_event?.(name, attrs);
  },
} as const;

/**
 * Default host facade. Plugin authors import this single symbol to
 * reach every capability, e.g. `host.kv.set("foo", "bar")`. The
 * sub-namespaces are also exported individually so tree-shakers can
 * drop the unused ones at build time.
 */
export const host = {
  log,
  nowMs,
  kv,
  db,
  cache,
  http,
  media,
  users,
  audit,
  secrets,
  cron,
  i18n,
  observe,
} as const;
