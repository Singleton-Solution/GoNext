/**
 * Plugin-slug auto-detection.
 *
 * The host emits an import map that resolves `@gonext/sdk` to a single
 * URL on the admin origin — typically `/_/runtime/sdk.mjs` — and serves
 * each plugin's web bundle from `/api/plugins/<slug>/web/...`. When a
 * plugin's bundle calls into the SDK at runtime, the SDK needs to know
 * WHICH plugin is asking so the REST shims can target the right
 * /api/plugins/{slug}/... sub-tree (see PR #136 / routes/routes.go).
 *
 * Strategies, in priority order, all evaluated lazily so a missing one
 * does not throw at module load:
 *
 *   1. The `import.meta.url` of the CALLING module — typically the
 *      plugin's own ES module, served from
 *      `/api/plugins/<slug>/web/<file>.mjs`. We extract the slug from
 *      that path. This is the strongest signal because the URL is
 *      issued by the host's static handler and cannot be forged from
 *      inside the page.
 *
 *   2. A `<script type="application/json" id="gn-plugin-context">`
 *      block on the page. The admin renders this whenever a plugin
 *      bundle is mounted (`apps/web/src/components/plugin-frontend-host`).
 *      Useful when the SDK is imported BEFORE the plugin module's own
 *      `import.meta.url` is observable (rare; happens during certain
 *      eager-evaluated import-map paths).
 *
 *   3. An explicit `window.__GN_PLUGIN_SLUG__` global. This is the
 *      developer-mode escape hatch — local dev usually serves the SDK
 *      from a Vite dev server where `import.meta.url` does not look
 *      like the production path. Plugin authors set the global before
 *      importing the SDK in tests / Storybook.
 *
 * None of these throws when the SDK is loaded in an environment that
 * is NOT a plugin context (e.g. when the admin imports the SDK
 * directly for its own UI scaffolding). Instead we return `null` and
 * let the REST shims surface a clear error if a slug-dependent
 * operation is invoked without one.
 *
 * The matched slug must follow the kebab-case rules the manifest
 * schema enforces (lower-case ASCII, digits, hyphens, length 1..64);
 * a path that contains anything else is rejected so a maliciously
 * crafted URL like `/api/plugins/..%2Fadmin/web/x.mjs` cannot trick
 * the SDK into addressing routes outside the plugin namespace.
 */

/**
 * Pattern that matches a plugin slug under the host-side static
 * handler. The capture group is the slug; the trailing `/web/`
 * segment is required (the host serves plugin static assets ONLY
 * under that path), which keeps the match anchored to the well-known
 * mount point.
 */
const PLUGIN_URL_PATTERN = /\/api\/plugins\/([a-z0-9][a-z0-9-]{0,63})\/web\//;

/**
 * Same shape as the manifest validator. We re-derive it here so the
 * SDK has zero runtime dependency on the manifest package — anything
 * we accept the host must have already approved at install time.
 */
const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;

/**
 * Cached slug, populated lazily on the first successful detection.
 * Subsequent calls return the cached value. The cache is reset by
 * `__resetSlugCache()` for tests.
 */
let cachedSlug: string | null | undefined = undefined;

/**
 * Public surface: returns the plugin slug for the current bundle, or
 * `null` if none of the detection strategies found one.
 *
 * The result is memoized; the detection pass runs at most once per
 * page lifetime. Plugins that have a legitimate need to override the
 * slug at runtime (development reloads, SSR-into-CSR handoff) should
 * set `window.__GN_PLUGIN_SLUG__` and then call `__resetSlugCache()`.
 *
 * NEVER throws. Slug-dependent helpers (host.posts, host.users, …)
 * throw their own typed error when called without a slug; this
 * function staying total means a "what plugin am I?" probe is safe
 * to call from anywhere, including module top-level evaluation.
 */
export function getSlug(): string | null {
  if (cachedSlug !== undefined) {
    return cachedSlug;
  }
  cachedSlug = detectSlug();
  return cachedSlug;
}

/**
 * Asserts a slug is available. The REST shims call this BEFORE
 * issuing a fetch so a "no slug" condition becomes a clear typed
 * error rather than a request to `/api/plugins//posts` that the
 * server would silently 404.
 *
 * Throws a `SlugRequiredError`; callers can `catch` on the class to
 * differentiate this from network / parse errors.
 */
export function requireSlug(): string {
  const slug = getSlug();
  if (slug === null) {
    throw new SlugRequiredError(
      '[@gonext/sdk] no plugin slug detected. Set window.__GN_PLUGIN_SLUG__ ' +
        'before importing the SDK, or call setSlug() explicitly.',
    );
  }
  return slug;
}

/**
 * Explicit override for environments where auto-detection cannot
 * succeed (Node test harnesses, Storybook, the admin's own SSR
 * pre-render pass). Validates against the slug pattern so a typo
 * fails loudly rather than at the first fetch.
 *
 * Calling `setSlug(null)` clears the cache, equivalent to
 * `__resetSlugCache()` but exposed under the public API.
 */
export function setSlug(slug: string | null): void {
  if (slug === null) {
    cachedSlug = null;
    return;
  }
  if (!SLUG_PATTERN.test(slug)) {
    throw new TypeError(
      `[@gonext/sdk] setSlug rejected ${JSON.stringify(slug)}; ` +
        'slug must match /^[a-z0-9][a-z0-9-]{0,63}$/.',
    );
  }
  cachedSlug = slug;
}

/**
 * Test-only escape hatch. Resets the cache so the next `getSlug`
 * call re-runs detection with whatever globals the test has set up.
 * Not part of the public API — production code that wants to swap
 * slugs uses `setSlug`.
 */
export function __resetSlugCache(): void {
  cachedSlug = undefined;
}

/**
 * Detection-error subclass. Plugin code can pattern-match on it to
 * skip slug-dependent setup when running in a non-plugin context:
 *
 *   try { return host.posts.list(); }
 *   catch (e) {
 *     if (e instanceof SlugRequiredError) return [];
 *     throw e;
 *   }
 */
export class SlugRequiredError extends Error {
  override readonly name = 'SlugRequiredError';
}

/**
 * Lazy, multi-strategy detection. Each branch returns `null` instead
 * of throwing so the next branch gets a shot.
 */
function detectSlug(): string | null {
  // Strategy 1: the URL of THIS module. tsup emits `import.meta.url`
  // verbatim into the ESM build, and the host serves the SDK from
  // `/_/runtime/sdk.mjs` — not under `/api/plugins/…` — so this
  // branch only ever matches when the SDK has been re-served under a
  // plugin's own bundle path (rare, but legal: a plugin can ship a
  // pinned SDK copy). For the common case the next branch handles
  // it.
  const selfURL = readImportMetaURL();
  if (selfURL !== null) {
    const match = PLUGIN_URL_PATTERN.exec(selfURL);
    if (match && typeof match[1] === 'string' && SLUG_PATTERN.test(match[1])) {
      return match[1];
    }
  }

  // Strategy 2: a `currentScript`-style readout. When a plugin bundle
  // is loaded the browser exposes `document.currentScript.src` to
  // synchronous top-level code. We scan the page's <script> tags for
  // one served from `/api/plugins/<slug>/web/…` and take its slug.
  const scriptSlug = detectSlugFromScripts();
  if (scriptSlug !== null) {
    return scriptSlug;
  }

  // Strategy 3: a server-rendered JSON context block. The admin's
  // plugin loader writes one of these to the page right before
  // mounting the plugin's frontend bundle (see
  // `apps/web/src/components/plugin-frontend-host`). Reading it does
  // not require Trusted Types because we only inspect `textContent`.
  const ctxSlug = detectSlugFromContextElement();
  if (ctxSlug !== null) {
    return ctxSlug;
  }

  // Strategy 4: explicit global. Dev / test escape hatch.
  const globalSlug = detectSlugFromGlobal();
  if (globalSlug !== null) {
    return globalSlug;
  }

  return null;
}

/**
 * Returns `import.meta.url` when this module was loaded as an ES
 * module, or `null` in the CJS / pre-ESM path. We read via the
 * indirected `getMetaURL` so the CJS build (which does not have
 * `import.meta`) does not fail to transpile.
 */
function readImportMetaURL(): string | null {
  try {
    // The CJS bundle replaces `import.meta` with a polyfill object;
    // the ESM bundle keeps the native one. Both shapes are safe to
    // read.
    const meta = (globalThis as { __gnSdkMeta?: { url?: string } }).__gnSdkMeta;
    if (meta && typeof meta.url === 'string') {
      return meta.url;
    }
  } catch {
    // fall through
  }
  // The real ESM build emits `import.meta.url` directly; tsup keeps
  // the syntax in the .mjs output. We use a `Function` constructor
  // trick to avoid a parse error in environments that do not support
  // `import.meta` (e.g. the CJS test path running through Node's
  // require chain).
  try {
    // eslint-disable-next-line @typescript-eslint/no-implied-eval, no-new-func
    const get = new Function('try { return import.meta.url; } catch { return null; }');
    const v = (get as () => unknown)();
    if (typeof v === 'string') {
      return v;
    }
  } catch {
    // fall through
  }
  return null;
}

/**
 * Scans `document.scripts` for one whose `src` matches the
 * /api/plugins/<slug>/web/ pattern. Returns the first slug found, or
 * `null`. We take the FIRST because the admin mounts plugin bundles
 * one at a time and a second match would imply two plugins on one
 * page — at which point per-call slug overrides are the right
 * answer, not auto-detection.
 */
function detectSlugFromScripts(): string | null {
  if (typeof document === 'undefined') {
    return null;
  }
  const scripts = document.getElementsByTagName('script');
  for (let i = 0; i < scripts.length; i++) {
    const s = scripts[i];
    if (!s) continue;
    const src = s.src;
    if (typeof src !== 'string' || src === '') continue;
    const match = PLUGIN_URL_PATTERN.exec(src);
    if (match && typeof match[1] === 'string' && SLUG_PATTERN.test(match[1])) {
      return match[1];
    }
  }
  return null;
}

/**
 * Reads the JSON context block. The admin writes the slug under
 * `slug` so this stays robust against the block growing other
 * fields. A malformed JSON body returns `null` rather than throwing
 * so SDK init never breaks on a stale element.
 */
function detectSlugFromContextElement(): string | null {
  if (typeof document === 'undefined') {
    return null;
  }
  const el = document.getElementById('gn-plugin-context');
  if (el === null) {
    return null;
  }
  const text = el.textContent;
  if (text === null || text === '') {
    return null;
  }
  try {
    const parsed = JSON.parse(text) as { slug?: unknown };
    if (typeof parsed.slug === 'string' && SLUG_PATTERN.test(parsed.slug)) {
      return parsed.slug;
    }
  } catch {
    // fall through
  }
  return null;
}

/**
 * Reads the `window.__GN_PLUGIN_SLUG__` global. Validates against
 * the slug pattern so an attacker who manages to set a window
 * property cannot redirect the SDK at routes outside the plugin
 * namespace.
 */
function detectSlugFromGlobal(): string | null {
  const g = globalThis as { __GN_PLUGIN_SLUG__?: unknown };
  const raw = g.__GN_PLUGIN_SLUG__;
  if (typeof raw === 'string' && SLUG_PATTERN.test(raw)) {
    return raw;
  }
  return null;
}
