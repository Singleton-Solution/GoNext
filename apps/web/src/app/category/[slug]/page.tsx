/**
 * Category archive route — `/category/<slug>/`.
 *
 * Mirrors WordPress's `/category/{slug}/` permalink. Resolves the
 * term by (taxonomy="category", slug) and lists its published posts.
 * The theme resolver walks `category-{slug}` → `category` →
 * `archive` → `index` for category requests (the resolver currently
 * routes everything through the `taxonomy` precedence chain — we
 * forward `taxonomy=category` so the templated `taxonomy-category-*`
 * candidates pick up correctly).
 *
 * The body envelope, cache policy, and pagination handling mirror the
 * author archive route — see lib/archive.ts.
 */
import { cookies } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import { fetchTermBySlug } from '@/lib/api';
import { renderArchiveBundle, parsePageParam } from '@/lib/archive';
import { renderNotFound } from '@/lib/render';
import { PublicShell } from '../../PublicShell';

export const dynamic = 'force-dynamic';

/** The taxonomy slug this route serves — surfaced as a constant so
 *  the tag route below stays a near-mirror copy with only this line
 *  changed. */
const TAXONOMY = 'category';

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
  return { title: `Category: ${term.name}` };
}

export default async function CategoryArchivePage(
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
    heading: `Category: ${term.name}`,
    description: term.description,
    basePath: `/category/${encodeURIComponent(term.slug)}`,
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
