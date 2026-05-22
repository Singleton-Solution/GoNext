/**
 * Author archive route — `/author/<slug>/`.
 *
 * Mirrors WordPress's `/author/{slug}/` permalink. Resolves the user
 * record by slug, fetches the paginated list of their published
 * posts, and asks the theme resolver which template basename to use
 * (the resolver walks `author-{slug}` → `author-{id}` → `author` →
 * `archive` → `index` from packages/go/theme/templates).
 *
 * Failure modes:
 *  - Missing author -> Next 404 (same envelope as the catch-all path).
 *  - Empty post list -> 200 with the "no posts yet" empty state. An
 *    author with zero posts isn't a 404; the archive page still paints.
 *  - API offline -> the archive bundle renders against the fallback
 *    theme + empty post list, never crashes the route.
 *
 * Cache policy and template envelope match the rest of the public
 * site (see lib/archive.ts and lib/render.ts).
 */
import { cookies } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import { fetchAuthorBySlug } from '@/lib/api';
import { renderArchiveBundle, parsePageParam } from '@/lib/archive';
import { renderNotFound } from '@/lib/render';
import { PublicShell } from '../../PublicShell';

export const dynamic = 'force-dynamic';

interface AuthorRouteParams {
  slug: string;
}

interface AuthorRouteSearch {
  /** `?page=N` — passed through to the archive feed for pagination. */
  page?: string | string[];
}

async function readCookieHeader(): Promise<string> {
  try {
    const store = await cookies();
    return store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    return '';
  }
}

export async function generateMetadata(
  { params }: { params: Promise<AuthorRouteParams> },
): Promise<Metadata> {
  const { slug } = await params;
  if (!slug) return { title: 'Not found' };
  const cookie = await readCookieHeader();
  const author = await fetchAuthorBySlug(slug, { cookie });
  if (!author) return { title: 'Not found' };
  return { title: `Posts by ${author.name}` };
}

export default async function AuthorArchivePage(
  {
    params,
    searchParams,
  }: {
    params: Promise<AuthorRouteParams>;
    searchParams: Promise<AuthorRouteSearch>;
  },
): Promise<ReactElement> {
  const { slug } = await params;
  const { page: pageParam } = await searchParams;
  const cookie = await readCookieHeader();

  // No slug = 404. Next's [slug] catch-all guarantees a non-empty
  // segment in practice, but the defensive check keeps the type
  // narrow and survives upstream changes to the route shape.
  if (!slug) {
    const result = await renderNotFound({ cookie });
    return (
      <PublicShell
        bodyHtml={result.html}
        cssCustomProperties={result.css}
        templateBasename={result.templateBasename}
      />
    );
  }

  const author = await fetchAuthorBySlug(slug, { cookie });
  if (!author) {
    // Render the themed 404 envelope instead of calling Next's
    // notFound() — same approach as the [...slug] catch-all so the
    // visitor still sees the site chrome.
    const result = await renderNotFound({ cookie });
    return (
      <PublicShell
        bodyHtml={result.html}
        cssCustomProperties={result.css}
        templateBasename={result.templateBasename}
      />
    );
  }

  const page = parsePageParam(pageParam);
  const result = await renderArchiveBundle({
    type: 'author',
    heading: `Posts by ${author.name}`,
    description: author.description,
    basePath: `/author/${encodeURIComponent(author.slug)}`,
    page,
    authorSlug: author.slug,
    authorId: author.id,
    cookie,
  });
  return (
    <PublicShell
      bodyHtml={result.html}
      cssCustomProperties={result.css}
      templateBasename={result.templateBasename}
    />
  );
}
