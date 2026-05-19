/**
 * Next.js configuration for @gonext/docs.
 *
 * The docs site is fundamentally a static site generator over the monorepo's
 * `docs/` and `adr/` markdown trees. We use the App Router with `@next/mdx`
 * so any `.mdx` page rendered from filesystem content gets full GFM (tables,
 * footnotes, task lists), heading anchors via rehype-slug, and code-block
 * syntax highlighting via shiki.
 *
 * Why these specific plugins:
 *  - `remark-gfm` — the docs use GFM tables and task lists liberally; without
 *    this they render as literal pipes.
 *  - `rehype-slug` + `rehype-autolink-headings` — heading anchors for deep
 *    links and the right-rail table of contents. The TOC component reads
 *    these ids back out at runtime.
 *  - Shiki runs inside the page component (see lib/mdx.ts), not as a rehype
 *    plugin, so we can tree-shake the grammars we don't need and avoid
 *    bundling shiki's WASM into every route.
 *
 * Output mode is the default (not standalone) — the site renders fully at
 * build time via `generateStaticParams`, so all routes are static HTML that
 * any CDN can serve.
 */
import createMDX from '@next/mdx';
import remarkGfm from 'remark-gfm';
import rehypeSlug from 'rehype-slug';
import rehypeAutolinkHeadings from 'rehype-autolink-headings';

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  pageExtensions: ['ts', 'tsx', 'mdx'],
  // The build script (scripts/sync-content.mjs) copies docs/ and adr/ into
  // ./content/ before Next.js starts — keep this directory in the outFile
  // tracing whitelist so the standalone build, if ever enabled, ships the
  // source markdown along with the rendered HTML for client-side search.
  outputFileTracingIncludes: {
    '/docs/[...slug]': ['./content/**/*'],
    '/adr/[...slug]': ['./content/**/*'],
  },
};

const withMDX = createMDX({
  extension: /\.mdx?$/,
  options: {
    remarkPlugins: [remarkGfm],
    rehypePlugins: [
      rehypeSlug,
      [
        rehypeAutolinkHeadings,
        {
          behavior: 'append',
          properties: { className: ['heading-anchor'], ariaLabel: 'Anchor' },
        },
      ],
    ],
  },
});

export default withMDX(nextConfig);
