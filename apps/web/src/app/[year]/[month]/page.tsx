/**
 * Month archive route — `/<year>/<month>/`.
 *
 * Mirrors WordPress's `/<year>/<month>/` permalink. Two segments,
 * both validated:
 *
 *   - `year`  4-digit integer in [1000, 9999]
 *   - `month` 1- or 2-digit integer in [1, 12]
 *
 * If either segment is shaped wrong (non-numeric, too long), the
 * request can't possibly be a date archive so we fall through to the
 * singular renderer with the original `<year>/<month>` joined slug —
 * preserving WordPress permalink compatibility for posts whose slug
 * happens to occupy two path segments.
 *
 * If both segments are date-shaped but out of range (year >= 10000,
 * month >= 13), we return the themed 404 instead of a 500.
 */
import { cookies } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import {
  renderArchiveBundle,
  parsePageParam,
  validateYear,
  validateMonth,
  formatDateHeading,
} from '@/lib/archive';
import { renderSingular, renderNotFound } from '@/lib/render';
import { PublicShell } from '../../PublicShell';

export const dynamic = 'force-dynamic';

interface MonthRouteParams {
  year: string;
  month: string;
}

interface MonthRouteSearch {
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

/** Both segments must look like an integer; otherwise the request is
 *  a singular slug, not a malformed date. We treat date-shaped-but-
 *  out-of-range as 404, everything else as fall-through. */
function isDateShaped(rawYear: string, rawMonth: string): boolean {
  return /^\d{4}$/.test(rawYear) && /^\d{1,2}$/.test(rawMonth);
}

export async function generateMetadata(
  { params }: { params: Promise<MonthRouteParams> },
): Promise<Metadata> {
  const { year: rawYear, month: rawMonth } = await params;
  const year = validateYear(rawYear);
  const month = validateMonth(rawMonth);
  if (year !== null && month !== null) {
    return { title: formatDateHeading(year, month) };
  }
  if (isDateShaped(rawYear, rawMonth)) {
    return { title: 'Not found' };
  }
  const cookie = await readCookieHeader();
  const result = await renderSingular(`${rawYear}/${rawMonth}`, { cookie });
  return { title: result.title };
}

export default async function MonthArchivePage(
  {
    params,
    searchParams,
  }: {
    params: Promise<MonthRouteParams>;
    searchParams: Promise<MonthRouteSearch>;
  },
): Promise<ReactElement> {
  const { year: rawYear, month: rawMonth } = await params;
  const { page: pageParam } = await searchParams;
  const cookie = await readCookieHeader();

  const year = validateYear(rawYear);
  const month = validateMonth(rawMonth);

  if (year === null || month === null) {
    if (isDateShaped(rawYear, rawMonth)) {
      const result = await renderNotFound({ cookie });
      return (
        <PublicShell
          bodyHtml={result.html}
          cssCustomProperties={result.css}
          templateBasename={result.templateBasename}
        />
      );
    }
    const result = await renderSingular(`${rawYear}/${rawMonth}`, { cookie });
    return (
      <PublicShell
        bodyHtml={result.html}
        cssCustomProperties={result.css}
        templateBasename={result.templateBasename}
      />
    );
  }

  const page = parsePageParam(pageParam);
  // Use the zero-padded form in the basePath so pagination links
  // round-trip identically (WP convention is /YYYY/MM/).
  const monthSlug = month.toString().padStart(2, '0');
  const result = await renderArchiveBundle({
    type: 'date',
    heading: formatDateHeading(year, month),
    basePath: `/${year}/${monthSlug}`,
    page,
    year,
    month,
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
