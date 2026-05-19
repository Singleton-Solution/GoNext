/**
 * Tests for the pure thread helpers — no DOM required.
 */
import { describe, it, expect } from 'vitest';
import { buildThread, readCookie, formatTimestamp } from './thread';
import type { PublicComment } from './types';

function mk(id: string, path: string, parentId?: string): PublicComment {
  return {
    id,
    post_id: 'p1',
    parent_id: parentId,
    path,
    depth: path.split('.').length,
    author_display_name: id,
    content: 'c-' + id,
    created_at: '2026-05-17T09:00:00Z',
  };
}

describe('buildThread', () => {
  it('groups children under parents by parent_id', () => {
    const tree = buildThread([
      mk('a', 'a'),
      mk('a1', 'a.a1', 'a'),
      mk('a2', 'a.a2', 'a'),
      mk('b', 'b'),
    ]);
    expect(tree).toHaveLength(2);
    expect(tree[0]?.comment.id).toBe('a');
    expect(tree[0]?.children).toHaveLength(2);
    expect(tree[0]?.children[0]?.comment.id).toBe('a1');
    expect(tree[0]?.children[1]?.comment.id).toBe('a2');
    expect(tree[1]?.comment.id).toBe('b');
  });

  it('promotes orphans (parent missing from list) to top-level', () => {
    const tree = buildThread([
      mk('child', 'p.child', 'p'), // parent "p" missing
    ]);
    expect(tree).toHaveLength(1);
    expect(tree[0]?.comment.id).toBe('child');
  });

  it('sorts siblings by ltree path', () => {
    const tree = buildThread([
      mk('z', 'z'),
      mk('a', 'a'),
      mk('m', 'm'),
    ]);
    expect(tree.map((n) => n.comment.id)).toEqual(['a', 'm', 'z']);
  });

  it('handles an empty input', () => {
    expect(buildThread([])).toEqual([]);
  });
});

describe('readCookie', () => {
  it('extracts the value by name', () => {
    expect(readCookie('csrf', 'csrf=abc.def; sessionid=xyz')).toBe('abc.def');
  });

  it('returns empty when missing', () => {
    expect(readCookie('csrf', 'sessionid=xyz')).toBe('');
  });

  it('returns empty for empty input', () => {
    expect(readCookie('csrf', '')).toBe('');
  });
});

describe('formatTimestamp', () => {
  it('formats a valid ISO timestamp', () => {
    const out = formatTimestamp('2026-05-17T09:00:00Z', 'en-US');
    expect(out.length).toBeGreaterThan(0);
  });

  it('returns input on invalid timestamp', () => {
    expect(formatTimestamp('not-a-date', 'en-US')).toBe('not-a-date');
  });

  it('returns empty string for empty input', () => {
    expect(formatTimestamp('', 'en-US')).toBe('');
  });
});
