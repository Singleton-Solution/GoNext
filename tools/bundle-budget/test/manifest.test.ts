/**
 * Tests for the Next.js manifest parser.
 *
 * Uses a hand-rolled fixture: a `.next` directory with a build-manifest.json
 * and a handful of static assets. The fixture is rebuilt fresh in a tmpdir
 * for each test so we don't leak state between runs.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import {
  classify,
  computeRouteSizes,
  gzippedSize,
  parseManifest,
} from '../src/manifest.ts';

function makeFixture(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bundle-budget-'));
  const nextDir = path.join(dir, '.next');
  fs.mkdirSync(path.join(nextDir, 'static', 'chunks'), { recursive: true });
  fs.mkdirSync(path.join(nextDir, 'static', 'css'), { recursive: true });
  fs.mkdirSync(path.join(nextDir, 'static', 'media'), { recursive: true });

  // Use highly-compressible payloads so we get a predictable gzipped size
  // (well below the raw byte count) for the JS asset, and a smaller CSS
  // payload so the assertions don't have to be loose.
  fs.writeFileSync(
    path.join(nextDir, 'static', 'chunks', 'app.js'),
    'a'.repeat(10_000),
  );
  fs.writeFileSync(
    path.join(nextDir, 'static', 'css', 'app.css'),
    'b'.repeat(2_000),
  );
  // A binary-ish font payload — gzip will barely compress this.
  fs.writeFileSync(
    path.join(nextDir, 'static', 'media', 'inter.woff2'),
    Buffer.from(Array.from({ length: 500 }, (_, i) => i % 256)),
  );

  const manifest = {
    pages: {
      '/': [
        'static/chunks/app.js',
        'static/css/app.css',
      ],
      '/posts/[id]/edit': [
        'static/chunks/app.js',
        'static/css/app.css',
        'static/media/inter.woff2',
      ],
    },
  };
  fs.writeFileSync(
    path.join(nextDir, 'build-manifest.json'),
    JSON.stringify(manifest),
  );
  return nextDir;
}

describe('classify', () => {
  it.each([
    ['app.js', 'js'],
    ['app.mjs', 'js'],
    ['app.css', 'css'],
    ['inter.woff2', 'font'],
    ['inter.woff', 'font'],
    ['inter.ttf', 'font'],
    ['inter.otf', 'font'],
    ['logo.svg', 'other'],
    ['no-ext', 'other'],
  ])('classifies %s as %s', (file, expected) => {
    expect(classify(file)).toBe(expected);
  });
});

describe('gzippedSize', () => {
  it('shrinks highly-repetitive input', () => {
    const raw = 'a'.repeat(10_000);
    expect(gzippedSize(raw)).toBeLessThan(200);
  });

  it('accepts a Buffer', () => {
    expect(gzippedSize(Buffer.from('abc'))).toBeGreaterThan(0);
  });

  it('returns non-zero for an empty input (gzip header)', () => {
    expect(gzippedSize('')).toBeGreaterThan(0);
  });
});

describe('parseManifest + computeRouteSizes', () => {
  let nextDir: string;

  beforeEach(() => {
    nextDir = makeFixture();
  });

  afterEach(() => {
    fs.rmSync(path.dirname(nextDir), { recursive: true, force: true });
  });

  it('parses the manifest and finds both routes', () => {
    const m = parseManifest(nextDir);
    expect(Object.keys(m.pages).sort()).toEqual([
      '/',
      '/posts/[id]/edit',
    ]);
  });

  it('throws when no manifest is present', () => {
    const empty = fs.mkdtempSync(path.join(os.tmpdir(), 'no-manifest-'));
    fs.mkdirSync(path.join(empty, '.next'));
    expect(() => parseManifest(path.join(empty, '.next'))).toThrow(
      /manifest/,
    );
    fs.rmSync(empty, { recursive: true, force: true });
  });

  it('computes per-asset-class sizes', () => {
    const m = parseManifest(nextDir);
    const sizes = computeRouteSizes(m, nextDir);
    const home = sizes.find((s) => s.route === '/');
    expect(home).toBeDefined();
    expect(home!.byKind.js).toBeGreaterThan(0);
    expect(home!.byKind.css).toBeGreaterThan(0);
    expect(home!.byKind.font).toBe(0);
    // Total is the sum of the kinds.
    const sumOfKinds =
      home!.byKind.js +
      home!.byKind.css +
      home!.byKind.font +
      home!.byKind.other;
    expect(home!.totalBytes).toBe(sumOfKinds);
  });

  it('records each file with its classification', () => {
    const m = parseManifest(nextDir);
    const sizes = computeRouteSizes(m, nextDir);
    const editor = sizes.find((s) => s.route === '/posts/[id]/edit');
    expect(editor?.files.length).toBe(3);
    const kinds = editor?.files.map((f) => f.kind).sort();
    expect(kinds).toEqual(['css', 'font', 'js']);
  });

  it('produces stable (alphabetical) route order', () => {
    const m = parseManifest(nextDir);
    const sizes = computeRouteSizes(m, nextDir);
    const order = sizes.map((s) => s.route);
    expect(order).toEqual([...order].sort());
  });

  it('records a zero-byte entry for missing files instead of crashing', () => {
    // Drop the JS asset but leave the manifest pointing at it.
    fs.unlinkSync(path.join(nextDir, 'static', 'chunks', 'app.js'));
    const m = parseManifest(nextDir);
    const sizes = computeRouteSizes(m, nextDir);
    const home = sizes.find((s) => s.route === '/');
    const jsEntry = home?.files.find((f) => f.kind === 'js');
    expect(jsEntry?.bytes).toBe(0);
  });

  it('merges app-router and pages-router manifests', () => {
    // Add an app-build-manifest with a route the pages manifest doesn't have.
    fs.writeFileSync(
      path.join(nextDir, 'app-build-manifest.json'),
      JSON.stringify({
        pages: { '/dashboard': ['static/chunks/app.js'] },
      }),
    );
    const m = parseManifest(nextDir);
    const sizes = computeRouteSizes(m, nextDir);
    const dashboard = sizes.find((s) => s.route === '/dashboard');
    expect(dashboard).toBeDefined();
    expect(dashboard!.byKind.js).toBeGreaterThan(0);
  });
});
