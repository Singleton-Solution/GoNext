/**
 * @gonext/admin — API client.
 *
 * Minimal fetch wrapper around the GoNext API. Responsibilities:
 *
 *  - Resolve the base URL from `NEXT_PUBLIC_API_URL` (default `http://localhost:8080`).
 *  - Always send credentials (`credentials: 'include'`) so the session cookie
 *    issued by the API server travels with each call. The API and admin
 *    typically live on different origins (`:8080` vs `:3001`) during dev, so
 *    CORS with `Access-Control-Allow-Credentials: true` and an explicit
 *    `Allow-Origin` for the admin origin must be configured on the API side.
 *  - JSON-encode requests with a body, JSON-decode 2xx responses.
 *  - Surface 4xx/5xx as a typed `ApiError` rather than returning the raw
 *    response. Callers `try { ... } catch (err) { if (err instanceof ApiError) ... }`.
 *  - Tolerate empty 204 responses (no JSON to decode).
 *
 * This file is intentionally thin: a richer client (typed endpoints, retry,
 * request IDs, etc.) lands in a follow-up issue once the OpenAPI surface is
 * stable.
 */

const DEFAULT_BASE_URL = 'http://localhost:8080';

/**
 * Resolve the API base URL. The admin app runs in two contexts:
 *
 *  - Browser (client): NEXT_PUBLIC_API_URL is baked into the bundle at
 *    build time. When empty (the docker-compose default), fetches hit
 *    the same origin and the Next.js `rewrites()` config proxies
 *    `/api/*` to the docker-internal API hostname.
 *  - Node (server): Server Components / Route Handlers / middleware
 *    run inside the admin container; the Next.js rewrites do NOT apply
 *    to outbound fetches from server code. We need an explicit
 *    docker-internal hostname — GONEXT_API_URL=http://api:8080.
 *
 * Empty string is a deliberate value (it means "same origin via
 * rewrites") and must NOT be coerced to the default. We use an
 * explicit `undefined` check rather than `||` to preserve that.
 *
 * The branch selection MUST happen at request time, not at module-load
 * time — see the docstring on `apiBaseUrl()` below for why.
 */
function pickNonUndefined(value: string | undefined): string | undefined {
  return value === undefined ? undefined : value;
}

/**
 * Resolve the API base URL at **runtime**.
 *
 * IMPORTANT: this MUST be called at request time, not module-load time.
 * Next.js bundles Client Components at build time, when
 * `typeof window === 'undefined'` evaluates to TRUE on the bundler's
 * Node process. If we cached the result in a module-load constant the
 * server branch would win and `http://api:8080` (the docker-internal
 * hostname) would be baked into every Client Component bundle,
 * producing browser-side DNS failures on every fetch. Keeping this as
 * a function means the `typeof window` check evaluates in the actual
 * runtime (browser → client branch, server-component render → server
 * branch). See issue #498.
 */
export function apiBaseUrl(): string {
  const isServer = typeof window === 'undefined';
  if (isServer) {
    // Server: prefer GONEXT_API_URL (docker-internal), fall back to the
    // public URL, and only use the localhost default as a last resort.
    const server = process.env.GONEXT_API_URL;
    if (server !== undefined && server !== '') return server;
    const pub = pickNonUndefined(process.env.NEXT_PUBLIC_API_URL);
    if (pub !== undefined) return pub;
    return DEFAULT_BASE_URL;
  }
  // Client: empty string is a legitimate "same-origin via rewrites"
  // configuration. Only fall through to DEFAULT when the var is unset.
  const pub = process.env.NEXT_PUBLIC_API_URL;
  return pub === undefined ? DEFAULT_BASE_URL : pub;
}

export type ApiMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export interface RequestOptions {
  method?: ApiMethod;
  body?: unknown;
  // Extra headers merged on top of the JSON defaults. Caller wins on conflict.
  headers?: Record<string, string>;
  // Forward an AbortSignal if the caller needs cancellation.
  signal?: AbortSignal;
}

/**
 * Error thrown for any non-2xx response. `payload` is the parsed JSON body
 * when the server returned one, else `undefined`.
 */
export class ApiError extends Error {
  public readonly status: number;
  public readonly statusText: string;
  public readonly payload: unknown;

  constructor(
    status: number,
    statusText: string,
    payload: unknown,
    message?: string,
  ) {
    super(message ?? `API error ${status}: ${statusText}`);
    this.name = 'ApiError';
    this.status = status;
    this.statusText = statusText;
    this.payload = payload;
  }
}

function joinUrl(base: string, path: string): string {
  const left = base.endsWith('/') ? base.slice(0, -1) : base;
  const right = path.startsWith('/') ? path : `/${path}`;
  return `${left}${right}`;
}

async function safeJson(response: Response): Promise<unknown> {
  if (response.status === 204) return undefined;
  const text = await response.text();
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

/**
 * Issue a request against the GoNext API and return the parsed JSON body.
 *
 * @throws {ApiError} when the response status is outside 2xx.
 */
export async function apiRequest<TResponse = unknown>(
  path: string,
  options: RequestOptions = {},
): Promise<TResponse> {
  const { method = 'GET', body, headers, signal } = options;

  const finalHeaders: Record<string, string> = {
    Accept: 'application/json',
    ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    ...headers,
  };

  const response = await fetch(joinUrl(apiBaseUrl(), path), {
    method,
    headers: finalHeaders,
    credentials: 'include',
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
  });

  const payload = await safeJson(response);

  if (!response.ok) {
    throw new ApiError(response.status, response.statusText, payload);
  }

  return payload as TResponse;
}

/** Convenience wrappers for the most common verbs. */
export const api = {
  get: <T = unknown>(path: string, options?: Omit<RequestOptions, 'method' | 'body'>) =>
    apiRequest<T>(path, { ...options, method: 'GET' }),
  post: <T = unknown>(
    path: string,
    body?: unknown,
    options?: Omit<RequestOptions, 'method' | 'body'>,
  ) => apiRequest<T>(path, { ...options, method: 'POST', body }),
  put: <T = unknown>(
    path: string,
    body?: unknown,
    options?: Omit<RequestOptions, 'method' | 'body'>,
  ) => apiRequest<T>(path, { ...options, method: 'PUT', body }),
  patch: <T = unknown>(
    path: string,
    body?: unknown,
    options?: Omit<RequestOptions, 'method' | 'body'>,
  ) => apiRequest<T>(path, { ...options, method: 'PATCH', body }),
  delete: <T = unknown>(
    path: string,
    options?: Omit<RequestOptions, 'method' | 'body'>,
  ) => apiRequest<T>(path, { ...options, method: 'DELETE' }),
};
