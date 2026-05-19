/**
 * Year archive route — `/<year>/`.
 *
 * Mirrors WordPress's `/<year>/` permalink. The route segment is named
 * `[year]` rather than e.g. `[slug]` so the parameter shape is
 * self-documenting; Next.js still resolves single-segment dynamic
 * paths the same way regardless of the placeholder name.
 *
 * Routing precedence (App Router):
 *
 *   /author/<slug>      -> author/[slug]/page.tsx (specific subtree)
 *   /category/<slug>    -> category/[slug]/page.tsx
 *   /tag/<slug>         -> tag/[slug]/page.tsx
 *   /<single-segment>   -> [year]/page.tsx          (this file)
 *   /<two-segments>     -> [year]/[month]/page.tsx
 *   /<three-segments>   -> [year]/[month]/[day]/page.tsx
 *   /<four-or-more>     -> [...slug]/page.tsx       (catch-all)
 *
 * Because the year route now owns every single-segment URL, naive
 * post slugs like `/hello-world` would 404 if we treated "not a year"
 * as a hard error. We instead delegate non-year-shaped slugs back to
 * `renderSingular` — exactly what the catch-all did before this route
 * existed. The semantics are:
 *
 *   - `/2026`     -> year archive
 *   - `/abc`      -> singular post lookup (fall through to renderer)
 *   - `/99999`    -> 404 (year-shaped but out of range)
 *   - `/202`      -> singular post lookup (not 4 digits)
 *
 * This preserves WordPress permalink compatibility: a /<year>/ URL
 * always wins for valid years, and arbitrary post slugs continue to
 * resolve via the singular path.
 */
import { cookies } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import {
  renderArchiveBundle,
  parsePageParam,
  validateYear,
  formatDateHeading,
} from '@/lib/archive';
import { renderSingular, renderNotFound } from '@/lib/render';
import { PublicShell } from '../PublicShell';

export const dynamic = 'force-dynamic';

interface YearRouteParams {
  year: string;
}

interface YearRouteSearch {
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

/** Detect "the segment looks like a year but is out of range". A bare
 *  4-digit number outside [1000, 9999] is genuinely malformed input
 *  rather than a slug. Anything else (non-numeric, too-short, etc.) is
 *  treated as a singular slug and forwarded to the renderer. */
function isYearShaped(raw: string): boolean {
  return /^\d{4}$/.test(raw);
}

export async function generateMetadata(
  { params }: { params: Promise<YearRouteParams> },
): Promise<Metadata> {
  const { year: rawYear } = await params;
  const year = validateYear(rawYear);
  if (year !== null) {
    return { title: formatDateHeading(year) };
  }
  if (isYearShaped(rawYear)) {
    return { title: 'Not found' };
  }
  // Fall through to singular metadata.
  const cookie = await readCookieHeader();
  const result = await renderSingular(rawYear, { cookie });
  return { title: result.title };
}

export default async function YearArchivePage(
  {
    params,
    searchParams,
  }: {
    params: Promise<YearRouteParams>;
    searchParams: Promise<YearRouteSearch>;
  },
): Promise<ReactElement> {
  const { year: rawYear } = await params;
  const { page: pageParam } = await searchParams;
  const cookie = await readCookieHeader();

  const year = validateYear(rawYear);
  if (year === null) {
    // Year-shaped but invalid (e.g. "99999" never matches; "1500" is
    // in range so doesn't get here) -> render the themed 404. Anything
    // not year-shaped is a singular post slug — fall through.
    if (isYearShaped(rawYear)) {
      const result = await renderNotFound({ cookie });
      return (
        <PublicShell
          bodyHtml={result.html}
          cssCustomProperties={result.css}
          templateBasename={result.templateBasename}
        />
      );
    }
    const result = await renderSingular(rawYear, { cookie });
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
    type: 'date',
    heading: formatDateHeading(year),
    basePath: `/${year}`,
    page,
    year,
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
