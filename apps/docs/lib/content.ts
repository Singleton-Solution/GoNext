/**
 * Filesystem walker and nav-tree builder for the docs site.
 *
 * The runtime model:
 *  - Source content lives under `apps/docs/content/{docs,adr}/...md` (synced
 *    from the monorepo's `docs/` and `adr/` at build time — see
 *    `scripts/sync-content.mjs`).
 *  - We walk that tree once per build and produce two artifacts:
 *      1. A nav tree per section, consumed by `<Sidebar />`.
 *      2. A flat list of `(section, slug)` pairs that
 *         `generateStaticParams` uses to pre-render every page.
 *
 * Why this lives in `lib/` and not in a server component: it's pure I/O
 * with no React dependencies, which keeps unit testing cheap.
 *
 * Slug shape: the URL path after `/docs/` or `/adr/` is the file path
 * relative to the section root, with the `.md`/`.mdx` extension stripped
 * and `index.md` collapsed to the parent directory. Sub-folders nest
 * naturally so `proposals/14-proposals.md` becomes `/docs/proposals/14-proposals`.
 */

import { promises as fs } from 'node:fs';
import { join, relative, sep } from 'node:path';
import matter from 'gray-matter';

export type Section = 'docs' | 'adr';

/**
 * One node in the sidebar tree. Files have `slug`; directories have
 * `children`. We never mix the two — a directory with an `index.md`
 * becomes a file with the directory's basename and no children.
 */
export interface NavNode {
  /** Display title — pulled from frontmatter, then the first H1, then filename. */
  title: string;
  /** Relative slug under the section, e.g. `00-architecture-overview`. */
  slug?: string;
  /** Subtree for directories. Undefined for files. */
  children?: NavNode[];
}

export interface PageMeta {
  section: Section;
  slug: string;
  /** Slug split on `/` — what `generateStaticParams` wants. */
  slugParts: string[];
  title: string;
  description?: string;
  /** Absolute path on disk; used by the page component to read the body. */
  filePath: string;
}

const CONTENT_ROOT_DEFAULT = join(process.cwd(), 'content');

/**
 * Resolve the on-disk root for a given section. Exported so tests can point
 * it at a fixture directory.
 */
export function sectionRoot(section: Section, contentRoot: string = CONTENT_ROOT_DEFAULT): string {
  return join(contentRoot, section);
}

/**
 * Extract a title from raw markdown. Order of preference:
 *  1. `title:` in YAML frontmatter.
 *  2. The first `# heading` in the body.
 *  3. Title-cased filename as a fallback.
 *
 * The frontmatter parse is lazy (only ever runs once per file per build),
 * but if we ever build incremental rebuilds, this is the obvious cache point.
 */
export function extractTitle(raw: string, filename: string): { title: string; description?: string } {
  const parsed = matter(raw);
  const fm = parsed.data as Record<string, unknown>;
  if (typeof fm.title === 'string' && fm.title.trim()) {
    return {
      title: fm.title.trim(),
      description: typeof fm.description === 'string' ? fm.description : undefined,
    };
  }
  // `gray-matter` strips frontmatter, so the H1 search runs against the body.
  const h1 = parsed.content.match(/^#\s+(.+?)\s*$/m);
  if (h1 && h1[1]) {
    return {
      title: h1[1].trim(),
      description: typeof fm.description === 'string' ? fm.description : undefined,
    };
  }
  // Filename fallback: `00-architecture-overview.md` → `Architecture Overview`.
  const base = filename.replace(/\.mdx?$/, '');
  const cleaned = base.replace(/^[\d_-]+/, '').replace(/[-_]+/g, ' ').trim() || base;
  return { title: cleaned.replace(/\b\w/g, (c) => c.toUpperCase()) };
}

/**
 * Compute the URL slug for a file relative to the section root.
 * `foo/bar.md`  -> `foo/bar`
 * `index.md`    -> `''` (the section landing page)
 * `foo/index.md`-> `foo`
 */
export function slugFor(relPath: string): string {
  let s = relPath.replace(/\.mdx?$/, '');
  // POSIX-style separators in URLs regardless of host platform.
  s = s.split(sep).join('/');
  if (s === 'index') return '';
  if (s.endsWith('/index')) return s.slice(0, -'/index'.length);
  return s;
}

interface FileEntry {
  absPath: string;
  /** Path relative to the section root (NOT the immediate parent dir). */
  relPath: string;
  name: string;
  raw: string;
}

/**
 * Walks a directory recursively. The `root` parameter is captured once at
 * the top-level call so nested entries report paths relative to the
 * section root — without that, `proposals/14-foo.md` would come back as
 * just `14-foo.md` and collide with top-level files of the same name.
 */
async function listMarkdown(dir: string, root: string = dir): Promise<FileEntry[]> {
  const out: FileEntry[] = [];
  let entries: import('node:fs').Dirent[];
  try {
    entries = await fs.readdir(dir, { withFileTypes: true });
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return [];
    throw err;
  }
  for (const ent of entries) {
    const abs = join(dir, ent.name);
    if (ent.isDirectory()) {
      // Skip the convention-only audit folders that aren't user docs.
      if (ent.name.startsWith('_')) continue;
      const sub = await listMarkdown(abs, root);
      out.push(...sub);
    } else if (ent.isFile() && /\.mdx?$/.test(ent.name)) {
      const raw = await fs.readFile(abs, 'utf8');
      out.push({ absPath: abs, relPath: relative(root, abs), name: ent.name, raw });
    }
  }
  return out;
}

/**
 * Build the flat page list for a section. Used by `generateStaticParams`
 * and by the search index builder on the client.
 */
export async function listPages(
  section: Section,
  contentRoot: string = CONTENT_ROOT_DEFAULT,
): Promise<PageMeta[]> {
  const root = sectionRoot(section, contentRoot);
  const entries = await listMarkdown(root);
  const pages: PageMeta[] = entries.map((e) => {
    const slug = slugFor(e.relPath);
    const { title, description } = extractTitle(e.raw, e.name);
    return {
      section,
      slug,
      slugParts: slug === '' ? [] : slug.split('/'),
      title,
      description,
      filePath: e.absPath,
    };
  });
  // Sort lexicographically — file prefixes like `00-`, `01-` give us the
  // correct reading order for free.
  pages.sort((a, b) => a.slug.localeCompare(b.slug));
  return pages;
}

/**
 * Build the hierarchical nav tree for a section. Directories become folder
 * nodes; markdown files become leaf nodes. A directory containing an
 * `index.md` becomes a single leaf (the directory itself is "the page").
 */
export async function buildNav(
  section: Section,
  contentRoot: string = CONTENT_ROOT_DEFAULT,
): Promise<NavNode[]> {
  const pages = await listPages(section, contentRoot);
  const root: NavNode = { title: section, children: [] };

  for (const page of pages) {
    const parts = page.slugParts.length === 0 ? ['index'] : page.slugParts;
    let cursor = root;
    for (let i = 0; i < parts.length; i++) {
      const isLeaf = i === parts.length - 1;
      const part = parts[i] as string;
      cursor.children = cursor.children ?? [];
      if (isLeaf) {
        cursor.children.push({ title: page.title, slug: page.slug });
      } else {
        let next = cursor.children.find((c) => !c.slug && c.title === part);
        if (!next) {
          next = { title: part, children: [] };
          cursor.children.push(next);
        }
        cursor = next;
      }
    }
  }
  return root.children ?? [];
}

/**
 * Resolve a single page by section + slug parts. Returns `null` if no
 * matching file exists — the page route uses this to call `notFound()`.
 */
export async function findPage(
  section: Section,
  slugParts: string[],
  contentRoot: string = CONTENT_ROOT_DEFAULT,
): Promise<{ meta: PageMeta; raw: string; body: string } | null> {
  const pages = await listPages(section, contentRoot);
  const wantedSlug = slugParts.join('/');
  const meta = pages.find((p) => p.slug === wantedSlug);
  if (!meta) return null;
  const raw = await fs.readFile(meta.filePath, 'utf8');
  const parsed = matter(raw);
  return { meta, raw, body: parsed.content };
}

/**
 * Lightweight client-shaped search index. Builds at request time on the
 * server side and is shipped to the client as a single JSON blob.
 */
export interface SearchEntry {
  section: Section;
  slug: string;
  title: string;
  description?: string;
}

export async function buildSearchIndex(
  contentRoot: string = CONTENT_ROOT_DEFAULT,
): Promise<SearchEntry[]> {
  const sections: Section[] = ['docs', 'adr'];
  const all: SearchEntry[] = [];
  for (const section of sections) {
    const pages = await listPages(section, contentRoot);
    for (const p of pages) {
      all.push({ section, slug: p.slug, title: p.title, description: p.description });
    }
  }
  return all;
}
