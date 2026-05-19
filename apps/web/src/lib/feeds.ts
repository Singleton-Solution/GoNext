/**
 * @gonext/web — feeds + sitemap builders.
 *
 * Pure functions that produce deterministic XML strings from typed
 * inputs. Kept free of network I/O so route handlers can compose
 * "fetch upstream data, hand it to a builder" pipelines, and so unit
 * tests can assert on the exact bytes the renderer would emit.
 *
 * Why a dedicated module rather than building XML inline in each
 * route:
 *
 *  1. XML escaping is load-bearing — a single un-escaped `&` in a
 *     post title kills feed-reader parsers. Centralizing the escape
 *     function gives us one place to audit and test.
 *  2. The sitemap format gets a tiny bit subtle once we have to
 *     paginate (the 50,000-URL hard cap from the sitemaps.org spec).
 *     Wrapping it in `buildSitemap` / `buildSitemapIndex` keeps the
 *     route handler small.
 *  3. Tests can hold the builders to a deterministic-output contract
 *     — same inputs always produce byte-identical output, with no
 *     stray clock reads or non-deterministic ordering.
 *
 * No emoji, no smart-quote normalization, no markdown-to-HTML — the
 * input contract is "give me trusted text, I'll XML-escape it." Block
 * rendering belongs in `blocks.ts` / `render.ts`.
 */

/**
 * sitemaps.org caps a single sitemap file at 50,000 URLs. When the
 * total exceeds this we emit a sitemap *index* that points at one
 * or more child sitemaps, each capped at this size. The constant is
 * exported so tests can drive the boundary without manufacturing
 * 50,001 fixture rows.
 */
export const SITEMAP_MAX_URLS_PER_FILE = 50_000;

/**
 * XML 1.0 forbids most C0 control characters in element content. We
 * strip anything outside the legal set rather than letting an upstream
 * row crash a feed reader's parser. The legal set is:
 *
 *   #x9 | #xA | #xD | [#x20-#xD7FF] | [#xE000-#xFFFD] | [#x10000-#x10FFFF]
 *
 * In practice we hit this only when bad data sneaks past the API
 * (CMS imports of legacy WordPress dumps are a known source).
 */
const xmlInvalidChars = /[^\x09\x0A\x0D\x20-퟿-�\u{10000}-\u{10FFFF}]/gu;

/**
 * Escape a string for safe inclusion in XML element content or an
 * attribute value. The five entities mandated by the XML spec, plus
 * a control-char strip so a stray U+0001 in a legacy post body can't
 * break feed parsers.
 *
 * Exported for tests; consumed by every builder below.
 */
export function escapeXml(value: string): string {
  return value
    .replace(xmlInvalidChars, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

/**
 * One entry in a sitemap. We deliberately keep the shape minimal —
 * sitemaps.org allows `priority` and `changefreq`, but every major
 * search engine has publicly stated they ignore those. Emitting them
 * just wastes bytes.
 */
export interface SitemapEntry {
  /** Absolute URL of the page. */
  loc: string;
  /** ISO-8601 timestamp (W3C-Datetime subset is also valid). Optional. */
  lastmod?: string;
}

/**
 * One entry in an Atom feed. Mirrors the fields RFC 4287 requires
 * (`id`, `title`, `updated`) plus the ones every reader expects in
 * practice (`link`, `author`, `summary` / `content`).
 *
 * Authors are emitted as a single `<author><name>...` block — multi-
 * author Atom is supported in the spec but the renderer's data model
 * is single-author per post; if that changes we'll widen the shape.
 */
export interface FeedEntry {
  /** Globally unique, time-stable identifier (we use the absolute URL). */
  id: string;
  /** Plain-text title (escaped on emission). */
  title: string;
  /** Absolute permalink to the post. */
  link: string;
  /** ISO-8601 `updated` (Atom mandates it; falls back to `published`). */
  updated: string;
  /** Optional ISO-8601 first-published timestamp. */
  published?: string;
  /** Author display name. Optional; readers fall back to feed-level author. */
  authorName?: string;
  /**
   * Short plain-text excerpt. Emitted as `<summary type="text">`.
   * Optional — entries without a summary still satisfy the spec.
   */
  summary?: string;
  /**
   * HTML body. Emitted as `<content type="html">` with an inner
   * CDATA so we don't have to escape the HTML twice. Optional;
   * many sites prefer summary-only feeds. The CDATA section is
   * itself escaped against `]]>` injection.
   */
  contentHtml?: string;
}

/**
 * Inputs to `buildAtomFeed`. Feed-level metadata maps 1:1 to the
 * Atom `<feed>` mandatory children.
 */
export interface AtomFeedOptions {
  /** Stable feed identifier — typically the canonical feed URL. */
  id: string;
  /** Feed display title. */
  title: string;
  /** Optional subtitle (rendered as `<subtitle>`). */
  subtitle?: string;
  /** Absolute URL of the site (the alternate `<link>`). */
  siteUrl: string;
  /** Absolute URL of this feed (the `rel="self"` `<link>`). */
  feedUrl: string;
  /**
   * Feed `<updated>` timestamp. Pass an explicit value to keep the
   * output deterministic in tests — otherwise the route handler
   * usually passes the most recent entry's timestamp or "now".
   */
  updated: string;
  /** Optional default author when an entry omits its own. */
  authorName?: string;
  /** Entries, already sorted newest-first by the caller. */
  entries: FeedEntry[];
}

/**
 * Escape the inside of an Atom `<content type="html">` CDATA block.
 *
 * CDATA accepts almost any byte verbatim except the literal `]]>`
 * terminator. We split on that sequence and rejoin with the standard
 * `]]]]><![CDATA[>` trick so the renderer can ship arbitrary HTML
 * (including raw `<` / `>`) without re-escaping each character.
 *
 * Exported for tests; in production it's only ever called from
 * `renderEntry`.
 */
export function escapeCdata(value: string): string {
  return value.replace(/]]>/g, ']]]]><![CDATA[>');
}

/**
 * Build an Atom 1.0 feed document. Deterministic: given identical
 * inputs the output is byte-identical, so a fixture-based test can
 * assert against a golden string.
 *
 * The renderer keeps emission narrow:
 *  - One namespace declaration (`xmlns="http://www.w3.org/2005/Atom"`).
 *  - No XSL stylesheet PI — most readers ignore it and it costs a
 *    round-trip the first time someone opens the URL in a browser.
 *  - Newline-separated children, no indentation. Smaller payload, and
 *    diff-friendlier than pretty-printed XML.
 */
export function buildAtomFeed(options: AtomFeedOptions): string {
  const parts: string[] = [];
  parts.push('<?xml version="1.0" encoding="UTF-8"?>');
  parts.push('<feed xmlns="http://www.w3.org/2005/Atom">');
  parts.push(`<id>${escapeXml(options.id)}</id>`);
  parts.push(`<title>${escapeXml(options.title)}</title>`);
  if (options.subtitle) {
    parts.push(`<subtitle>${escapeXml(options.subtitle)}</subtitle>`);
  }
  parts.push(`<updated>${escapeXml(options.updated)}</updated>`);
  parts.push(
    `<link rel="self" type="application/atom+xml" href="${escapeXml(
      options.feedUrl,
    )}"/>`,
  );
  parts.push(
    `<link rel="alternate" type="text/html" href="${escapeXml(
      options.siteUrl,
    )}"/>`,
  );
  if (options.authorName) {
    parts.push(`<author><name>${escapeXml(options.authorName)}</name></author>`);
  }
  for (const entry of options.entries) {
    parts.push(renderEntry(entry));
  }
  parts.push('</feed>');
  return parts.join('\n');
}

function renderEntry(entry: FeedEntry): string {
  const parts: string[] = [];
  parts.push('<entry>');
  parts.push(`<id>${escapeXml(entry.id)}</id>`);
  parts.push(`<title>${escapeXml(entry.title)}</title>`);
  parts.push(
    `<link rel="alternate" type="text/html" href="${escapeXml(entry.link)}"/>`,
  );
  parts.push(`<updated>${escapeXml(entry.updated)}</updated>`);
  if (entry.published) {
    parts.push(`<published>${escapeXml(entry.published)}</published>`);
  }
  if (entry.authorName) {
    parts.push(`<author><name>${escapeXml(entry.authorName)}</name></author>`);
  }
  if (entry.summary) {
    parts.push(`<summary type="text">${escapeXml(entry.summary)}</summary>`);
  }
  if (entry.contentHtml) {
    // CDATA lets us ship the HTML body verbatim. The escape only
    // touches the CDATA terminator, not the HTML itself, so
    // `<p>Hello &amp; world</p>` round-trips byte-for-byte.
    parts.push(
      `<content type="html"><![CDATA[${escapeCdata(entry.contentHtml)}]]></content>`,
    );
  }
  parts.push('</entry>');
  return parts.join('\n');
}

/**
 * Build a single sitemap.xml file. Caller guarantees `entries.length`
 * does not exceed [SITEMAP_MAX_URLS_PER_FILE]; for larger sets, call
 * [buildSitemapIndex] + emit per-page sitemaps via this same builder.
 *
 * The output omits `<priority>` and `<changefreq>` deliberately —
 * Google, Bing, and Yandex have publicly confirmed they ignore those
 * hints. Shipping them only inflates the payload.
 *
 * `lastmod` is optional per-entry. When absent the element is simply
 * omitted; we do NOT stamp "now" so cached output stays cacheable.
 */
export function buildSitemap(entries: SitemapEntry[]): string {
  if (entries.length > SITEMAP_MAX_URLS_PER_FILE) {
    throw new Error(
      `buildSitemap: ${entries.length} entries exceeds the per-file cap of ${SITEMAP_MAX_URLS_PER_FILE}; use buildSitemapIndex`,
    );
  }
  const parts: string[] = [];
  parts.push('<?xml version="1.0" encoding="UTF-8"?>');
  parts.push('<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">');
  for (const entry of entries) {
    parts.push('<url>');
    parts.push(`<loc>${escapeXml(entry.loc)}</loc>`);
    if (entry.lastmod) {
      parts.push(`<lastmod>${escapeXml(entry.lastmod)}</lastmod>`);
    }
    parts.push('</url>');
  }
  parts.push('</urlset>');
  return parts.join('\n');
}

/**
 * Build a sitemap index that points at one or more child sitemaps.
 * The Sitemap protocol allows up to 50,000 entries per index file
 * (same cap as the URL count); we don't enforce that here because
 * the renderer's data volume is nowhere near it — if and when a
 * deployment crosses 2.5 billion URLs the index can itself be
 * split.
 *
 * Each child entry receives an optional `lastmod` so search engines
 * can skip refetching unchanged pages.
 */
export function buildSitemapIndex(entries: SitemapEntry[]): string {
  const parts: string[] = [];
  parts.push('<?xml version="1.0" encoding="UTF-8"?>');
  parts.push('<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">');
  for (const entry of entries) {
    parts.push('<sitemap>');
    parts.push(`<loc>${escapeXml(entry.loc)}</loc>`);
    if (entry.lastmod) {
      parts.push(`<lastmod>${escapeXml(entry.lastmod)}</lastmod>`);
    }
    parts.push('</sitemap>');
  }
  parts.push('</sitemapindex>');
  return parts.join('\n');
}

/**
 * Inputs to [buildRobotsTxt].
 *
 * `allowIndex=true` emits the public-site convention — let everything
 * be crawled and point at the sitemap. `false` emits the staging /
 * preview convention — disallow everything, omit the sitemap line so
 * crawlers that ignore Disallow still don't discover content URLs.
 */
export interface RobotsTxtOptions {
  /** When true, emit Allow-everything + Sitemap line. */
  allowIndex: boolean;
  /** Absolute URL of the sitemap (typically `${baseUrl}/sitemap.xml`). */
  sitemapUrl?: string;
}

/**
 * Build a robots.txt body. Single-User-agent (`*`) — per-bot rules
 * are explicitly out of scope here; an operator who needs them can
 * front the renderer with their CDN's bot management.
 *
 * Deterministic: same inputs always produce byte-identical output.
 * Trailing newline included (POSIX text-file convention; some
 * crawlers historically choked on the last line without one).
 */
export function buildRobotsTxt(options: RobotsTxtOptions): string {
  const lines: string[] = [];
  lines.push('User-agent: *');
  if (options.allowIndex) {
    lines.push('Allow: /');
    if (options.sitemapUrl) {
      lines.push('');
      lines.push(`Sitemap: ${options.sitemapUrl}`);
    }
  } else {
    // Staging / preview: keep the renderer out of search results.
    // We deliberately omit the Sitemap line — a crawler that ignores
    // Disallow shouldn't be handed a list of indexable URLs.
    lines.push('Disallow: /');
  }
  return lines.join('\n') + '\n';
}

/**
 * Helper for routes that need to split a giant entry set into pages
 * of [SITEMAP_MAX_URLS_PER_FILE]. Returns a list of pages; the route
 * handler renders the index from `pages.length > 1`, or the single
 * sitemap from `pages[0]`.
 */
export function paginateSitemap(entries: SitemapEntry[]): SitemapEntry[][] {
  if (entries.length <= SITEMAP_MAX_URLS_PER_FILE) {
    return [entries];
  }
  const out: SitemapEntry[][] = [];
  for (let i = 0; i < entries.length; i += SITEMAP_MAX_URLS_PER_FILE) {
    out.push(entries.slice(i, i + SITEMAP_MAX_URLS_PER_FILE));
  }
  return out;
}
