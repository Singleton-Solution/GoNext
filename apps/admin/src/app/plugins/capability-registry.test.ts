/**
 * capability-registry — unit tests.
 *
 * The TypeScript mirror must stay in lockstep with the Go-side built-in
 * list. These tests pin the surface so a future change here is
 * deliberate: any rename or removal will land in a diff that touches
 * this file plus `packages/go/plugins/capabilities/registry.go`.
 */
import { describe, expect, it } from 'vitest';

import {
  allKnownCapabilities,
  lookupCapability,
  resolveCapabilities,
  unknownCapability,
} from './capability-registry';

describe('capability-registry', () => {
  it('returns the canonical description for known capabilities', () => {
    const cap = lookupCapability('posts.read');
    expect(cap).not.toBeNull();
    expect(cap?.id).toBe('posts.read');
    expect(cap?.resource).toBe('posts');
    expect(cap?.action).toBe('read');
    expect(cap?.human).toMatch(/read all posts/i);
    expect(cap?.risk).toBe('low');
  });

  it('marks email.send and http.fetch as sensitive', () => {
    expect(lookupCapability('email.send')?.risk).toBe('sensitive');
    expect(lookupCapability('http.fetch')?.risk).toBe('sensitive');
  });

  it('returns null for unknown ids', () => {
    expect(lookupCapability('not.real')).toBeNull();
  });

  it('synthesises a sensitive unknown row for unrecognised ids', () => {
    const cap = unknownCapability('vendor.exotic');
    expect(cap.id).toBe('vendor.exotic');
    expect(cap.risk).toBe('sensitive');
    expect(cap.human).toMatch(/not recognised/i);
  });

  it('resolves a list in declaration order, surfacing unknowns inline', () => {
    const resolved = resolveCapabilities(['posts.read', 'not.real', 'kv.write']);
    expect(resolved.map((c) => c.id)).toEqual([
      'posts.read',
      'not.real',
      'kv.write',
    ]);
    expect(resolved[1]!.risk).toBe('sensitive'); // unknown flagged sensitive
  });

  it('lists every built-in capability, sorted by id', () => {
    const all = allKnownCapabilities();
    const ids = all.map((c) => c.id);
    // Spot-check the known set.
    expect(ids).toContain('posts.read');
    expect(ids).toContain('email.send');
    expect(ids).toContain('jobs.enqueue');
    // Sorted ascending.
    const sorted = [...ids].sort((a, b) => a.localeCompare(b));
    expect(ids).toEqual(sorted);
  });
});
