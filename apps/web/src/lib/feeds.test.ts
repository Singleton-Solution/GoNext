/**
 * Tests for the pure feed + sitemap builders.
 *
 * These are deterministic-output tests — given identical inputs the
 * builders must produce byte-identical output, so we can assert on
 * exact strings rather than parsing the XML back. That lets a single
 * regression in escaping or ordering trip a test immediately.
 */
import { describe, it, expect } from 'vitest';
import {
  escapeXml,
  escapeCdata,
  buildAtomFeed,
  buildSitemap,
  buildSitemapIndex,
  buildRobotsTxt,
  paginateSitemap,
  SITEMAP_MAX_URLS_PER_FILE,
  type FeedEntry,
  type SitemapEntry,
} from './feeds.ts';

describe('escapeXml', () => {
  it('escapes the five XML entities', () => {
    expect(escapeXml('&')).toBe('&amp;');
    expect(escapeXml('<')).toBe('&lt;');
    expect(escapeXml('>')).toBe('&gt;');
    expect(escapeXml('"')).toBe('&quot;');
    expect(escapeXml("'")).toBe('&apos;');
  });

  it('orders ampersand escape first to avoid double-escape', () => {
    // If `<` were escaped before `&`, the result would re-escape the
    // ampersand in the entity. Catch that regression here.
    expect(escapeXml('<&>')).toBe('&lt;&amp;&gt;');
  });

  it('strips XML-1.0-illegal control chars', () => {
    // U+0001 is illegal in XML 1.0 element content.
    expect(escapeXml('ab')).toBe('ab');
  });

  it('preserves legal whitespace', () => {
    expect(escapeXml('a\tb\nc\rd')).toBe('a\tb\nc\rd');
  });

  it('passes plain ASCII through unchanged', () => {
    expect(escapeXml('hello world')).toBe('hello world');
  });
});

describe('escapeCdata', () => {
  it('passes a normal HTML body through unchanged', () => {
    expect(escapeCdata('<p>Hello &amp; world</p>')).toBe(
      '<p>Hello &amp; world</p>',
    );
  });

  it('splits a CDATA terminator', () => {
    // The CDATA terminator must be split so the consumer's XML parser
    // doesn't end the section early.
    const got = escapeCdata('foo]]>bar');
    expect(got).toBe('foo]]]]><![CDATA[>bar');
  });
});

describe('buildSitemap', () => {
  it('produces a well-formed empty urlset for zero entries', () => {
    const xml = buildSitemap([]);
    expect(xml).toContain('<?xml version="1.0" encoding="UTF-8"?>');
    expect(xml).toContain(
      '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    );
    expect(xml).toContain('</urlset>');
    expect(xml).not.toContain('<url>');
  });

  it('emits one <url> per entry', () => {
    const xml = buildSitemap([
      { loc: 'https://example.com/a' },
      { loc: 'https://example.com/b' },
    ]);
    expect((xml.match(/<url>/g) ?? []).length).toBe(2);
    expect(xml).toContain('<loc>https://example.com/a</loc>');
    expect(xml).toContain('<loc>https://example.com/b</loc>');
  });

  it('includes lastmod when present', () => {
    const xml = buildSitemap([
      { loc: 'https://example.com/a', lastmod: '2026-05-19T00:00:00Z' },
    ]);
    expect(xml).toContain('<lastmod>2026-05-19T00:00:00Z</lastmod>');
  });

  it('omits lastmod when absent', () => {
    const xml = buildSitemap([{ loc: 'https://example.com/a' }]);
    expect(xml).not.toContain('<lastmod>');
  });

  it('escapes special chars in loc', () => {
    const xml = buildSitemap([
      { loc: 'https://example.com/a?x=1&y=2' },
    ]);
    expect(xml).toContain('https://example.com/a?x=1&amp;y=2');
  });

  it('produces deterministic output for the same input', () => {
    const entries: SitemapEntry[] = [
      { loc: 'https://example.com/a' },
      { loc: 'https://example.com/b' },
    ];
    expect(buildSitemap(entries)).toBe(buildSitemap(entries));
  });

  it('throws when handed more than the per-file cap', () => {
    const entries: SitemapEntry[] = Array.from(
      { length: SITEMAP_MAX_URLS_PER_FILE + 1 },
      (_, i) => ({ loc: `https://example.com/${i}` }),
    );
    expect(() => buildSitemap(entries)).toThrow(/per-file cap/);
  });

  it('handles 100 entries cleanly', () => {
    const entries: SitemapEntry[] = Array.from({ length: 100 }, (_, i) => ({
      loc: `https://example.com/post-${i}`,
    }));
    const xml = buildSitemap(entries);
    expect((xml.match(/<url>/g) ?? []).length).toBe(100);
    // First and last should both be present in order
    expect(xml.indexOf('post-0')).toBeLessThan(xml.indexOf('post-99'));
  });
});

describe('paginateSitemap', () => {
  it('returns a single page when entries fit under the cap', () => {
    const pages = paginateSitemap([
      { loc: 'https://example.com/a' },
    ]);
    expect(pages).toHaveLength(1);
    expect(pages[0]).toHaveLength(1);
  });

  it('splits into ceil(n/cap) pages', () => {
    const entries = Array.from(
      { length: SITEMAP_MAX_URLS_PER_FILE + 100 },
      (_, i) => ({ loc: `https://example.com/${i}` }),
    );
    const pages = paginateSitemap(entries);
    expect(pages).toHaveLength(2);
    expect(pages[0]).toHaveLength(SITEMAP_MAX_URLS_PER_FILE);
    expect(pages[1]).toHaveLength(100);
  });
});

describe('buildSitemapIndex', () => {
  it('emits a sitemapindex with one <sitemap> per child', () => {
    const xml = buildSitemapIndex([
      { loc: 'https://example.com/sitemap-1.xml' },
      { loc: 'https://example.com/sitemap-2.xml' },
    ]);
    expect(xml).toContain('<sitemapindex');
    expect((xml.match(/<sitemap>/g) ?? []).length).toBe(2);
    expect(xml).toContain('https://example.com/sitemap-1.xml');
  });
});

describe('buildAtomFeed', () => {
  const baseOptions = {
    id: 'https://example.com/feed.xml',
    title: 'Test feed',
    siteUrl: 'https://example.com',
    feedUrl: 'https://example.com/feed.xml',
    updated: '2026-05-19T00:00:00Z',
  };

  it('produces a well-formed empty feed', () => {
    const xml = buildAtomFeed({ ...baseOptions, entries: [] });
    expect(xml).toContain('<?xml version="1.0" encoding="UTF-8"?>');
    expect(xml).toContain('<feed xmlns="http://www.w3.org/2005/Atom">');
    expect(xml).toContain('</feed>');
    expect(xml).not.toContain('<entry>');
  });

  it('includes mandatory feed-level elements', () => {
    const xml = buildAtomFeed({ ...baseOptions, entries: [] });
    expect(xml).toContain('<id>https://example.com/feed.xml</id>');
    expect(xml).toContain('<title>Test feed</title>');
    expect(xml).toContain('<updated>2026-05-19T00:00:00Z</updated>');
    expect(xml).toContain(
      '<link rel="self" type="application/atom+xml" href="https://example.com/feed.xml"/>',
    );
    expect(xml).toContain(
      '<link rel="alternate" type="text/html" href="https://example.com"/>',
    );
  });

  it('includes one <entry> per input', () => {
    const entries: FeedEntry[] = [
      {
        id: 'https://example.com/a',
        title: 'A',
        link: 'https://example.com/a',
        updated: '2026-05-19T00:00:00Z',
      },
      {
        id: 'https://example.com/b',
        title: 'B',
        link: 'https://example.com/b',
        updated: '2026-05-18T00:00:00Z',
      },
    ];
    const xml = buildAtomFeed({ ...baseOptions, entries });
    expect((xml.match(/<entry>/g) ?? []).length).toBe(2);
  });

  it('escapes XML special chars in titles', () => {
    const entries: FeedEntry[] = [
      {
        id: 'https://example.com/x',
        title: 'Tom & Jerry: "An <Adventure>"',
        link: 'https://example.com/x',
        updated: '2026-05-19T00:00:00Z',
      },
    ];
    const xml = buildAtomFeed({ ...baseOptions, entries });
    expect(xml).toContain(
      '<title>Tom &amp; Jerry: &quot;An &lt;Adventure&gt;&quot;</title>',
    );
    // The plaintext form must not leak through.
    expect(xml).not.toContain('Tom & Jerry: "An <Adventure>"');
  });

  it('wraps contentHtml in CDATA verbatim', () => {
    const entries: FeedEntry[] = [
      {
        id: 'https://example.com/x',
        title: 'Hello',
        link: 'https://example.com/x',
        updated: '2026-05-19T00:00:00Z',
        contentHtml: '<p>Hi <strong>there</strong></p>',
      },
    ];
    const xml = buildAtomFeed({ ...baseOptions, entries });
    expect(xml).toContain(
      '<content type="html"><![CDATA[<p>Hi <strong>there</strong></p>]]></content>',
    );
  });

  it('emits author and summary when present', () => {
    const entries: FeedEntry[] = [
      {
        id: 'https://example.com/x',
        title: 'Hi',
        link: 'https://example.com/x',
        updated: '2026-05-19T00:00:00Z',
        authorName: 'Ada Lovelace',
        summary: 'a short excerpt',
      },
    ];
    const xml = buildAtomFeed({ ...baseOptions, entries });
    expect(xml).toContain('<author><name>Ada Lovelace</name></author>');
    expect(xml).toContain('<summary type="text">a short excerpt</summary>');
  });

  it('produces deterministic output', () => {
    const entries: FeedEntry[] = [
      {
        id: 'https://example.com/a',
        title: 'A',
        link: 'https://example.com/a',
        updated: '2026-05-19T00:00:00Z',
      },
    ];
    expect(buildAtomFeed({ ...baseOptions, entries })).toBe(
      buildAtomFeed({ ...baseOptions, entries }),
    );
  });
});

describe('buildRobotsTxt', () => {
  it('emits allow-everything + sitemap when allowIndex=true', () => {
    const txt = buildRobotsTxt({
      allowIndex: true,
      sitemapUrl: 'https://example.com/sitemap.xml',
    });
    expect(txt).toContain('User-agent: *');
    expect(txt).toContain('Allow: /');
    expect(txt).toContain('Sitemap: https://example.com/sitemap.xml');
    expect(txt).not.toContain('Disallow:');
  });

  it('omits Sitemap line when no sitemapUrl', () => {
    const txt = buildRobotsTxt({ allowIndex: true });
    expect(txt).toContain('Allow: /');
    expect(txt).not.toContain('Sitemap:');
  });

  it('emits Disallow: / when allowIndex=false', () => {
    const txt = buildRobotsTxt({ allowIndex: false });
    expect(txt).toContain('User-agent: *');
    expect(txt).toContain('Disallow: /');
    // Even if a sitemap URL were passed, it must NOT appear.
    expect(txt).not.toContain('Sitemap:');
  });

  it('does not leak a sitemap URL when disallowed', () => {
    // Defense-in-depth: passing a sitemap URL alongside allowIndex=false
    // (a caller bug) must still not expose it. The builder picks the
    // disallow branch and ignores the URL.
    const txt = buildRobotsTxt({
      allowIndex: false,
      sitemapUrl: 'https://example.com/sitemap.xml',
    });
    expect(txt).not.toContain('Sitemap:');
    expect(txt).not.toContain('example.com');
  });

  it('produces deterministic output ending with a newline', () => {
    const txt = buildRobotsTxt({ allowIndex: true });
    expect(txt.endsWith('\n')).toBe(true);
    expect(buildRobotsTxt({ allowIndex: true })).toBe(
      buildRobotsTxt({ allowIndex: true }),
    );
  });
});
