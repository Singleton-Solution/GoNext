/**
 * Tests for the import-map generator + manifest validator.
 *
 * The wire format is sensitive: the browser silently ignores
 * malformed import maps, so a tiny serialization drift can produce a
 * page that loads but rejects all plugin imports at runtime. We pin
 * the bytes here to catch that early.
 */
import { describe, expect, it } from 'vitest';
import {
  defaultAdminAllowlist,
  generateImportMap,
  validatePluginManifest,
  type PluginImportMapAllowlist,
} from './import-map';

const SAMPLE_ALLOWLIST: PluginImportMapAllowlist = {
  imports: [
    { specifier: '@gonext/sdk', url: '/_/runtime/sdk.mjs' },
    { specifier: 'react', url: '/_/runtime/react.mjs' },
  ],
};

describe('generateImportMap', () => {
  it('serializes a flat allowlist into the spec shape', () => {
    const doc = generateImportMap(SAMPLE_ALLOWLIST);
    expect(doc.parsed.imports['@gonext/sdk']).toBe('/_/runtime/sdk.mjs');
    expect(doc.parsed.imports['react']).toBe('/_/runtime/react.mjs');
    expect(doc.parsed.scopes).toBeUndefined();
  });

  it('emits two-space-indented JSON with a trailing newline (reproducible)', () => {
    const doc = generateImportMap(SAMPLE_ALLOWLIST);
    expect(doc.json.endsWith('\n')).toBe(true);
    expect(doc.json).toContain('  "imports"');
  });

  it('preserves caller-supplied insertion order in imports', () => {
    const doc = generateImportMap({
      imports: [
        { specifier: 'z-mod', url: '/z' },
        { specifier: 'a-mod', url: '/a' },
      ],
    });
    const keys = Object.keys(doc.parsed.imports);
    expect(keys).toEqual(['z-mod', 'a-mod']);
  });

  it('sorts scope URLs alphabetically (stable serialization)', () => {
    const scopes = new Map([
      [
        '/plugins/b/',
        [{ specifier: 'mod-b', url: '/b/mod' }],
      ],
      [
        '/plugins/a/',
        [{ specifier: 'mod-a', url: '/a/mod' }],
      ],
    ]);
    const doc = generateImportMap({ imports: [], scopes });
    expect(Object.keys(doc.parsed.scopes!)).toEqual(['/plugins/a/', '/plugins/b/']);
  });

  it('rejects duplicate specifiers loudly', () => {
    expect(() =>
      generateImportMap({
        imports: [
          { specifier: 'react', url: '/react-a' },
          { specifier: 'react', url: '/react-b' },
        ],
      }),
    ).toThrowError(/duplicate import-map specifier/);
  });

  it('rejects entries with empty specifier', () => {
    expect(() =>
      generateImportMap({
        imports: [{ specifier: '', url: '/x' }],
      }),
    ).toThrowError(/missing specifier/);
  });

  it('rejects entries with empty url', () => {
    expect(() =>
      generateImportMap({
        imports: [{ specifier: 'a', url: '' }],
      }),
    ).toThrowError(/missing url/);
  });

  it('omits scopes when scope map is empty', () => {
    const doc = generateImportMap({ imports: [], scopes: new Map() });
    expect(doc.parsed.scopes).toBeUndefined();
  });
});

describe('validatePluginManifest', () => {
  it('accepts a manifest whose imports are all in the allowlist', () => {
    const report = validatePluginManifest(SAMPLE_ALLOWLIST, {
      imports: ['@gonext/sdk', 'react'],
    });
    expect(report.ok).toBe(true);
  });

  it('rejects a manifest that imports an unmapped specifier', () => {
    const report = validatePluginManifest(SAMPLE_ALLOWLIST, {
      imports: ['@gonext/sdk', 'lodash'],
    });
    expect(report.ok).toBe(false);
    expect(report.offending).toContain('lodash');
    if (report.ok === false) {
      expect(report.reason).toContain('lodash');
    }
  });

  it('returns ok=true when manifest has no imports', () => {
    const report = validatePluginManifest(SAMPLE_ALLOWLIST, {});
    expect(report.ok).toBe(true);
  });

  it('rejects empty-string specifiers', () => {
    const report = validatePluginManifest(SAMPLE_ALLOWLIST, {
      imports: [''] as unknown as string[],
    });
    expect(report.ok).toBe(false);
  });

  it('accepts specifiers granted only by scope', () => {
    const allowlist: PluginImportMapAllowlist = {
      imports: [],
      scopes: new Map([
        ['/plugins/me/', [{ specifier: 'my-internal', url: '/plugins/me/x.mjs' }]],
      ]),
    };
    const report = validatePluginManifest(allowlist, { imports: ['my-internal'] });
    expect(report.ok).toBe(true);
  });

  it('rejects unmapped specifiers even when scopes exist', () => {
    const allowlist: PluginImportMapAllowlist = {
      imports: [],
      scopes: new Map([
        ['/plugins/me/', [{ specifier: 'my-internal', url: '/plugins/me/x.mjs' }]],
      ]),
    };
    const report = validatePluginManifest(allowlist, { imports: ['outside'] });
    expect(report.ok).toBe(false);
  });
});

describe('defaultAdminAllowlist', () => {
  it('includes @gonext/sdk so plugin code can call the SDK', () => {
    const a = defaultAdminAllowlist();
    expect(a.imports.find((e) => e.specifier === '@gonext/sdk')).toBeDefined();
  });

  it('includes the trusted-types installer specifier', () => {
    const a = defaultAdminAllowlist();
    expect(
      a.imports.find(
        (e) => e.specifier === '@gonext/plugin-frontend-host/trusted-types',
      ),
    ).toBeDefined();
  });

  it('produces a generator-valid allowlist', () => {
    const doc = generateImportMap(defaultAdminAllowlist());
    expect(JSON.parse(doc.json).imports).toBeTypeOf('object');
  });
});
