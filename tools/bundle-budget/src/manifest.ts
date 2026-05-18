/**
 * Next.js build-manifest parser.
 *
 * The Next.js build emits `.next/build-manifest.json` (App Router routes still
 * appear in the page-style sibling `.next/app-build-manifest.json` when
 * `appDir` is enabled — both have the same shape: `{ pages: { [route]:
 * string[] } }` mapping each route to the static asset paths it loads).
 * We don't depend on any Next.js internals; we just read the JSON and resolve
 * asset paths relative to `.next/`.
 *
 * Sizes are computed by gzipping each asset on disk with the standard zlib
 * compressor at level 9 (mirrors what the CDN will ship for `Accept-Encoding:
 * gzip`; we don't model brotli here — it would be a separate budget knob
 * and most CDNs serve both). The result is bucketed by asset kind so the
 * budget can target JS, CSS, and font ceilings independently.
 */
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as zlib from 'node:zlib';

export type AssetKind = 'js' | 'css' | 'font' | 'other';

/**
 * Strongly-typed mirror of the subset of `.next/build-manifest.json` we read.
 * `pages` is the per-route asset list. We do not look at the other top-level
 * keys (`rootMainFiles`, `polyfillFiles`, …) — those become per-route entries
 * when actually used.
 */
export interface Manifest {
  pages: Record<string, string[]>;
  /**
   * Optional app-router siblings, when present. Same shape as `pages`.
   * Some Next.js versions split the output between `build-manifest.json`
   * (pages-router) and `app-build-manifest.json` (app-router); the parser
   * accepts either or both and merges them.
   */
  appPages?: Record<string, string[]>;
}

export interface RouteSize {
  route: string;
  /** Total gzipped bytes across all assets for this route. */
  totalBytes: number;
  /** Per-asset-class gzipped bytes. */
  byKind: Record<AssetKind, number>;
  /** Per-file breakdown, useful for failure diffs. */
  files: Array<{ path: string; kind: AssetKind; bytes: number }>;
}

/**
 * Classify an asset by its filename extension. We deliberately treat anything
 * unknown as `other` rather than throwing — Next.js occasionally adds new
 * asset categories and we don't want a build to break because we haven't
 * taught the budget about them yet.
 */
export function classify(file: string): AssetKind {
  const ext = path.extname(file).toLowerCase();
  if (ext === '.js' || ext === '.mjs') return 'js';
  if (ext === '.css') return 'css';
  if (
    ext === '.woff' ||
    ext === '.woff2' ||
    ext === '.ttf' ||
    ext === '.otf'
  ) {
    return 'font';
  }
  return 'other';
}

/**
 * Compute the gzipped size of a buffer in bytes, using zlib at level 9.
 * Exposed separately so tests can exercise the size math without touching
 * the filesystem.
 */
export function gzippedSize(input: Buffer | string): number {
  const buf = typeof input === 'string' ? Buffer.from(input) : input;
  return zlib.gzipSync(buf, { level: 9 }).length;
}

/**
 * Read and parse a Next.js build manifest from disk. If both the pages-router
 * and app-router manifests are present, both are merged into a single view.
 *
 * @param nextDir Path to the `.next` directory produced by `next build`.
 */
export function parseManifest(nextDir: string): Manifest {
  const pagesManifestPath = path.join(nextDir, 'build-manifest.json');
  const appManifestPath = path.join(nextDir, 'app-build-manifest.json');

  let pages: Record<string, string[]> = {};
  let appPages: Record<string, string[]> | undefined;

  if (fs.existsSync(pagesManifestPath)) {
    const raw = fs.readFileSync(pagesManifestPath, 'utf8');
    const parsed = JSON.parse(raw) as Partial<Manifest>;
    pages = parsed.pages ?? {};
  }

  if (fs.existsSync(appManifestPath)) {
    const raw = fs.readFileSync(appManifestPath, 'utf8');
    const parsed = JSON.parse(raw) as Partial<Manifest>;
    appPages = parsed.pages ?? {};
  }

  if (
    !fs.existsSync(pagesManifestPath) &&
    !fs.existsSync(appManifestPath)
  ) {
    throw new Error(
      `No Next.js build manifest found in ${nextDir}. Expected ` +
        `build-manifest.json or app-build-manifest.json. Did the build run?`,
    );
  }

  const result: Manifest = { pages };
  if (appPages) result.appPages = appPages;
  return result;
}

/**
 * Walk a manifest and compute per-route gzipped sizes. Asset paths are resolved
 * relative to `nextDir`; missing assets cause the route to record a zero-byte
 * file entry rather than crash — this keeps a partial build (e.g. cached
 * intermediate state) inspectable, and the missing-file shows up in the
 * markdown table for human review.
 */
export function computeRouteSizes(
  manifest: Manifest,
  nextDir: string,
): RouteSize[] {
  const combined: Record<string, string[]> = { ...manifest.pages };
  if (manifest.appPages) {
    for (const [route, files] of Object.entries(manifest.appPages)) {
      // Routes can appear in both manifests if the app uses a mix of routers.
      // Merge the file lists and dedupe.
      const existing = combined[route] ?? [];
      combined[route] = Array.from(new Set([...existing, ...files]));
    }
  }

  const routes: RouteSize[] = [];
  for (const [route, files] of Object.entries(combined)) {
    const byKind: Record<AssetKind, number> = {
      js: 0,
      css: 0,
      font: 0,
      other: 0,
    };
    const fileEntries: RouteSize['files'] = [];
    let total = 0;

    for (const rel of files) {
      const abs = path.join(nextDir, rel);
      const kind = classify(rel);
      let bytes = 0;
      if (fs.existsSync(abs)) {
        const buf = fs.readFileSync(abs);
        bytes = gzippedSize(buf);
      }
      byKind[kind] += bytes;
      total += bytes;
      fileEntries.push({ path: rel, kind, bytes });
    }

    routes.push({
      route,
      totalBytes: total,
      byKind,
      files: fileEntries,
    });
  }

  // Stable ordering: alphabetical by route so the markdown output diffs cleanly
  // between runs.
  routes.sort((a, b) => a.route.localeCompare(b.route));
  return routes;
}
