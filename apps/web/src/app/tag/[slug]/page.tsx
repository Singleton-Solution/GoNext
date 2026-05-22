/**
 * Tag archive route — `/tag/<slug>/`.
 *
 * Mirrors WordPress's `/tag/{slug}/` permalink. Identical to the
 * category route except the taxonomy slug is `post_tag` (WP's
 * canonical name for the tag taxonomy). The theme resolver walks
 * `tag-{slug}` → `tag` → `archive` → `index`; since the resolver
 * currently routes both built-in taxonomies through the generic
 * `taxonomy` chain, we forward `taxonomy=post_tag` so the
 * `taxonomy-post_tag-*` candidates resolve.
 */
import { cookies } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import { fetchTermBySlug } from '@/lib/api';
import { renderArchiveBundle, parsePageParam } from '@/lib/archive';
import { renderNotFound } from '@/lib/render';
import { PublicShell } from '../../PublicShell';

export const dynamic = 'force-dynamic';

const TAXONOMY = 'post_tag';

interface TaxonomyRouteParams {
  slug: string;
}

interface TaxonomyRouteSearch {
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
  { params }: { params: Promise<TaxonomyRouteParams> },
): Promise<Metadata> {
  const { slug } = await params;
  if (!slug) return { title: 'Not found' };
  const cookie = await readCookieHeader();
  const term = await fetchTermBySlug(TAXONOMY, slug, { cookie });
  if (!term) return { title: 'Not found' };
  return { title: `Tag: ${term.name}` };
}

export default async function TagArchivePage(
  {
    params,
    searchParams,
  }: {
    params: Promise<TaxonomyRouteParams>;
    searchParams: Promise<TaxonomyRouteSearch>;
  },
): Promise<ReactElement> {
  const { slug } = await params;
  const { page: pageParam } = await searchParams;
  const cookie = await readCookieHeader();

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

  const term = await fetchTermBySlug(TAXONOMY, slug, { cookie });
  if (!term) {
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
    type: 'taxonomy',
    heading: `Tag: ${term.name}`,
    description: term.description,
    basePath: `/tag/${encodeURIComponent(term.slug)}`,
    page,
    taxonomy: term.taxonomy,
    termSlug: term.slug,
    termId: term.id,
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
