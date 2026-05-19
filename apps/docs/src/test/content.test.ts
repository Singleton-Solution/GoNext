/**
 * Tests for the filesystem walker and nav-tree builder.
 *
 * The walker is pure I/O over a directory tree; each test creates a small
 * fixture, runs `listPages` / `buildNav` against it, and asserts on the
 * shape. We do not mock fs — the fixtures are cheap to spin up and the
 * real I/O path is what ships.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  buildNav,
  buildSearchIndex,
  extractTitle,
  findPage,
  listPages,
  slugFor,
} from '@/lib/content';
import { cleanupFixture, makeFixture } from './fixtures';

describe('slugFor', () => {
  it('strips the .md extension', () => {
    expect(slugFor('foo.md')).toBe('foo');
    expect(slugFor('foo.mdx')).toBe('foo');
  });

  it('collapses index.md to the parent path', () => {
    expect(slugFor('index.md')).toBe('');
    expect(slugFor('proposals/index.md')).toBe('proposals');
  });

  it('preserves nested paths', () => {
    expect(slugFor('proposals/14-foo.md')).toBe('proposals/14-foo');
  });
});

describe('extractTitle', () => {
  it('uses the title field from frontmatter when present', () => {
    const raw = `---\ntitle: Hello World\n---\n\n# Different Heading\n`;
    expect(extractTitle(raw, 'foo.md').title).toBe('Hello World');
  });

  it('falls back to the first H1', () => {
    const raw = `# My Page\n\nBody.\n`;
    expect(extractTitle(raw, 'foo.md').title).toBe('My Page');
  });

  it('falls back to the filename when no H1 or frontmatter title', () => {
    const raw = `Just a paragraph.\n`;
    expect(extractTitle(raw, '00-architecture-overview.md').title).toBe('Architecture Overview');
  });

  it('parses an optional description from frontmatter', () => {
    const raw = `---\ntitle: T\ndescription: Short desc\n---\n\nBody`;
    const out = extractTitle(raw, 'foo.md');
    expect(out.description).toBe('Short desc');
  });
});

describe('listPages', () => {
  let root: string;
  afterEach(async () => {
    if (root) await cleanupFixture(root);
  });

  it('walks a flat directory and returns ordered pages', async () => {
    root = await makeFixture([
      { path: 'docs/01-second.md', body: '# Second\n' },
      { path: 'docs/00-first.md', body: '# First\n' },
    ]);
    const pages = await listPages('docs', root);
    expect(pages.map((p) => p.slug)).toEqual(['00-first', '01-second']);
    expect(pages[0]?.title).toBe('First');
  });

  it('handles nested directories', async () => {
    root = await makeFixture([
      { path: 'docs/00-overview.md', body: '# Overview\n' },
      { path: 'docs/proposals/14-foo.md', body: '# Foo Proposal\n' },
    ]);
    const pages = await listPages('docs', root);
    expect(pages.map((p) => p.slug)).toEqual(['00-overview', 'proposals/14-foo']);
    expect(pages[1]?.slugParts).toEqual(['proposals', '14-foo']);
  });

  it('skips _audit and other underscore-prefixed dirs', async () => {
    root = await makeFixture([
      { path: 'docs/00-overview.md', body: '# Overview\n' },
      { path: 'docs/_audit/notes.md', body: '# Notes\n' },
    ]);
    const pages = await listPages('docs', root);
    expect(pages.map((p) => p.slug)).toEqual(['00-overview']);
  });

  it('returns empty when section is missing', async () => {
    root = await makeFixture([{ path: 'docs/x.md', body: '# X' }]);
    const pages = await listPages('adr', root);
    expect(pages).toEqual([]);
  });
});

describe('buildNav', () => {
  let root: string;
  afterEach(async () => {
    if (root) await cleanupFixture(root);
  });

  it('builds a flat tree for non-nested sections', async () => {
    root = await makeFixture([
      { path: 'docs/00-first.md', body: '# First\n' },
      { path: 'docs/01-second.md', body: '# Second\n' },
    ]);
    const nav = await buildNav('docs', root);
    expect(nav.map((n) => n.title)).toEqual(['First', 'Second']);
    for (const n of nav) {
      expect(n.children).toBeUndefined();
      expect(typeof n.slug).toBe('string');
    }
  });

  it('groups nested files under a folder node', async () => {
    root = await makeFixture([
      { path: 'docs/00-overview.md', body: '# Overview\n' },
      { path: 'docs/proposals/14-foo.md', body: '# Foo\n' },
      { path: 'docs/proposals/15-bar.md', body: '# Bar\n' },
    ]);
    const nav = await buildNav('docs', root);
    const folder = nav.find((n) => n.children && n.children.length > 1);
    expect(folder).toBeDefined();
    expect(folder?.title).toBe('proposals');
    expect(folder?.children?.map((c) => c.title)).toEqual(['Foo', 'Bar']);
  });
});

describe('findPage', () => {
  let root: string;
  afterEach(async () => {
    if (root) await cleanupFixture(root);
  });

  beforeEach(async () => {
    root = await makeFixture([
      { path: 'docs/00-overview.md', body: '---\ntitle: Overview\n---\n\n## Intro\n\nHello\n' },
    ]);
  });

  it('returns body and meta for a matching slug', async () => {
    const page = await findPage('docs', ['00-overview'], root);
    expect(page).not.toBeNull();
    expect(page?.meta.title).toBe('Overview');
    expect(page?.body).toContain('## Intro');
    // gray-matter strips the frontmatter block from `body`.
    expect(page?.body).not.toContain('title: Overview');
  });

  it('returns null when no file matches', async () => {
    const page = await findPage('docs', ['no-such-page'], root);
    expect(page).toBeNull();
  });
});

describe('buildSearchIndex', () => {
  let root: string;
  afterEach(async () => {
    if (root) await cleanupFixture(root);
  });

  it('returns entries from both sections combined', async () => {
    root = await makeFixture([
      { path: 'docs/00-overview.md', body: '# Overview\n' },
      { path: 'adr/0001-licensing.md', body: '# ADR 1\n' },
    ]);
    const entries = await buildSearchIndex(root);
    const sections = new Set(entries.map((e) => e.section));
    expect(sections.has('docs')).toBe(true);
    expect(sections.has('adr')).toBe(true);
    expect(entries.find((e) => e.title === 'Overview')).toBeDefined();
    expect(entries.find((e) => e.title === 'ADR 1')).toBeDefined();
  });
});
