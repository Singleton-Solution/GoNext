/**
 * Validation tests for the TypeScript-side manifest builder.
 *
 * The host-side schema in `packages/go/plugins/manifest/schema.json`
 * is authoritative; these tests pin the same constraints on the
 * TypeScript surface so a plugin author catches mistakes locally
 * BEFORE shipping the bundle.
 */

import { describe, expect, it } from 'vitest';

import {
  MANIFEST_API_VERSION,
  ManifestError,
  buildManifest,
  manifestToJSON,
  type ManifestInput,
} from '../src/manifest.ts';

function minimal(over?: Partial<ManifestInput>): ManifestInput {
  return {
    name: 'gn-hello',
    version: '0.1.0',
    entry: 'plugin.wasm',
    ...over,
  };
}

describe('buildManifest — happy path', () => {
  it('produces a canonical manifest with apiVersion injected', () => {
    const m = buildManifest(minimal());
    expect(m.apiVersion).toBe(MANIFEST_API_VERSION);
    expect(m.name).toBe('gn-hello');
    expect(m.version).toBe('0.1.0');
    expect(m.entry).toBe('plugin.wasm');
  });

  it('omits empty optional collections from the output', () => {
    const m = buildManifest(
      minimal({ capabilities: [], jobs: [], hooks: { actions: [] } }),
    );
    expect(m.capabilities).toBeUndefined();
    expect(m.jobs).toBeUndefined();
    expect(m.hooks).toBeUndefined();
  });

  it('round-trips through manifestToJSON', () => {
    const m = buildManifest(
      minimal({
        capabilities: ['kv.read', 'kv.write'],
        hooks: { actions: ['save_post'], filters: ['the_content'] },
        requires: { host: '>=0.1.0' },
      }),
    );
    const json = manifestToJSON(m);
    const reparsed = JSON.parse(json);
    expect(reparsed.apiVersion).toBe(MANIFEST_API_VERSION);
    expect(reparsed.capabilities).toEqual(['kv.read', 'kv.write']);
    expect(reparsed.hooks.actions).toEqual(['save_post']);
    expect(reparsed.hooks.filters).toEqual(['the_content']);
    expect(reparsed.requires).toEqual({ host: '>=0.1.0' });
  });
});

describe('buildManifest — name validation', () => {
  it.each([
    ['ab', 'too short'],
    ['Hello', 'uppercase letter'],
    ['hello!', 'illegal character'],
    ['-leading-hyphen', 'leading hyphen'],
    ['1numeric-start', 'numeric start'],
  ])('rejects %s (%s)', (name) => {
    expect(() => buildManifest(minimal({ name }))).toThrow(ManifestError);
  });

  it('accepts a typical slug', () => {
    expect(() => buildManifest(minimal({ name: 'gn-seo-pro' }))).not.toThrow();
  });
});

describe('buildManifest — semver validation', () => {
  it('accepts strict semver', () => {
    expect(() => buildManifest(minimal({ version: '1.2.3' }))).not.toThrow();
    expect(() =>
      buildManifest(minimal({ version: '1.0.0-beta.1+exp.sha.5114f85' })),
    ).not.toThrow();
  });

  it('rejects loose versions', () => {
    expect(() => buildManifest(minimal({ version: '1.2' }))).toThrow();
    expect(() => buildManifest(minimal({ version: '01.2.3' }))).toThrow();
    expect(() => buildManifest(minimal({ version: 'v1.2.3' }))).toThrow();
  });
});

describe('buildManifest — entry validation', () => {
  it('rejects parent-traversal segments', () => {
    expect(() => buildManifest(minimal({ entry: '../plugin.wasm' }))).toThrow(
      ManifestError,
    );
    expect(() =>
      buildManifest(minimal({ entry: 'subdir/../plugin.wasm' })),
    ).toThrow(ManifestError);
  });

  it('requires a .wasm extension', () => {
    expect(() => buildManifest(minimal({ entry: 'plugin.js' }))).toThrow();
  });

  it('accepts nested POSIX paths', () => {
    expect(() =>
      buildManifest(minimal({ entry: 'dist/plugin.wasm' })),
    ).not.toThrow();
  });
});

describe('buildManifest — capability validation', () => {
  it('rejects non-dotted-token capabilities', () => {
    expect(() =>
      buildManifest(minimal({ capabilities: ['Kv.Read'] })),
    ).toThrow();
    expect(() =>
      buildManifest(minimal({ capabilities: ['kv.read.write!'] })),
    ).toThrow();
  });

  it('rejects duplicate capabilities', () => {
    try {
      buildManifest(
        minimal({ capabilities: ['kv.read', 'kv.read', 'kv.write'] }),
      );
      expect.fail('should have thrown');
    } catch (err) {
      expect(err).toBeInstanceOf(ManifestError);
      const issues = (err as ManifestError).issues;
      expect(
        issues.find((i) => i.path === '/capabilities')?.message,
      ).toMatch(/unique/);
    }
  });
});

describe('buildManifest — hook validation', () => {
  it('accepts underscores in hook names', () => {
    expect(() =>
      buildManifest(
        minimal({ hooks: { actions: ['save_post', 'wp_head'] } }),
      ),
    ).not.toThrow();
  });

  it('rejects empty hook names', () => {
    expect(() =>
      buildManifest(minimal({ hooks: { actions: [''] } })),
    ).toThrow();
  });

  it('rejects duplicates', () => {
    expect(() =>
      buildManifest(
        minimal({ hooks: { actions: ['save_post', 'save_post'] } }),
      ),
    ).toThrow();
  });
});

describe('buildManifest — signature validation', () => {
  it('accepts 128 lowercase hex chars', () => {
    const sig = 'a'.repeat(128);
    expect(() => buildManifest(minimal({ signature: sig }))).not.toThrow();
  });

  it('rejects uppercase hex', () => {
    expect(() =>
      buildManifest(minimal({ signature: 'A'.repeat(128) })),
    ).toThrow();
  });

  it('rejects wrong length', () => {
    expect(() =>
      buildManifest(minimal({ signature: 'a'.repeat(127) })),
    ).toThrow();
  });
});

describe('buildManifest — storage validation', () => {
  it('accepts numeric quotas', () => {
    const m = buildManifest(
      minimal({ storage: { kv: { max_bytes: 1024, max_keys: 100 } } }),
    );
    expect(m.storage?.kv?.max_bytes).toBe(1024);
    expect(m.storage?.kv?.max_keys).toBe(100);
  });

  it('rejects negative or fractional quotas', () => {
    expect(() =>
      buildManifest(minimal({ storage: { kv: { max_bytes: -1 } } })),
    ).toThrow();
    expect(() =>
      buildManifest(minimal({ storage: { kv: { max_keys: 1.5 } } })),
    ).toThrow();
  });
});

describe('ManifestError', () => {
  it('aggregates every issue in a single throw', () => {
    try {
      buildManifest({
        name: 'Bad-Slug',
        version: 'nope',
        entry: 'plugin.wasm',
      });
      expect.fail('should have thrown');
    } catch (err) {
      expect(err).toBeInstanceOf(ManifestError);
      const e = err as ManifestError;
      expect(e.issues.length).toBeGreaterThanOrEqual(2);
      const paths = e.issues.map((i) => i.path);
      expect(paths).toContain('/name');
      expect(paths).toContain('/version');
    }
  });
});
