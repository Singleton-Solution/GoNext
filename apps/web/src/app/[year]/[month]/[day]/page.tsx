/**
 * Day archive route — `/<year>/<month>/<day>/`.
 *
 * Mirrors WordPress's `/<year>/<month>/<day>/` permalink. Three
 * segments, all validated:
 *
 *   - `year`  4-digit integer in [1000, 9999]
 *   - `month` 1- or 2-digit integer in [1, 12]
 *   - `day`   1- or 2-digit integer in [1, 31]
 *
 * Per-month day-count enforcement (Feb 30 etc.) is delegated to the
 * upstream API — when the day doesn't exist in the calendar the post
 * list comes back empty and the archive paints the empty state.
 *
 * If any segment is shaped wrong (non-numeric, too long), the request
 * can't be a date archive so we fall through to the singular renderer
 * with the original `<year>/<month>/<day>` joined slug. This is the
 * 3-segment equivalent of the year/month fallback and keeps three-
 * segment post slugs working.
 */
import { cookies } from 'next/headers';
import type { Metadata } from 'next';
import type { ReactElement } from 'react';
import {
  renderArchiveBundle,
  parsePageParam,
  validateYear,
  validateMonth,
  validateDay,
  formatDateHeading,
} from '@/lib/archive';
import { renderSingular, renderNotFound } from '@/lib/render';
import { PublicShell } from '../../../PublicShell';

export const dynamic = 'force-dynamic';

interface DayRouteParams {
  year: string;
  month: string;
  day: string;
}

interface DayRouteSearch {
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

function isDateShaped(rawYear: string, rawMonth: string, rawDay: string): boolean {
  return (
    /^\d{4}$/.test(rawYear) &&
    /^\d{1,2}$/.test(rawMonth) &&
    /^\d{1,2}$/.test(rawDay)
  );
}

export async function generateMetadata(
  { params }: { params: Promise<DayRouteParams> },
): Promise<Metadata> {
  const { year: rawYear, month: rawMonth, day: rawDay } = await params;
  const year = validateYear(rawYear);
  const month = validateMonth(rawMonth);
  const day = validateDay(rawDay);
  if (year !== null && month !== null && day !== null) {
    return { title: formatDateHeading(year, month, day) };
  }
  if (isDateShaped(rawYear, rawMonth, rawDay)) {
    return { title: 'Not found' };
  }
  const cookie = await readCookieHeader();
  const result = await renderSingular(`${rawYear}/${rawMonth}/${rawDay}`, { cookie });
  return { title: result.title };
}

export default async function DayArchivePage(
  {
    params,
    searchParams,
  }: {
    params: Promise<DayRouteParams>;
    searchParams: Promise<DayRouteSearch>;
  },
): Promise<ReactElement> {
  const { year: rawYear, month: rawMonth, day: rawDay } = await params;
  const { page: pageParam } = await searchParams;
  const cookie = await readCookieHeader();

  const year = validateYear(rawYear);
  const month = validateMonth(rawMonth);
  const day = validateDay(rawDay);

  if (year === null || month === null || day === null) {
    if (isDateShaped(rawYear, rawMonth, rawDay)) {
      const result = await renderNotFound({ cookie });
      return (
        <PublicShell
          bodyHtml={result.html}
          cssCustomProperties={result.css}
          templateBasename={result.templateBasename}
        />
      );
    }
    const result = await renderSingular(`${rawYear}/${rawMonth}/${rawDay}`, { cookie });
    return (
      <PublicShell
        bodyHtml={result.html}
        cssCustomProperties={result.css}
        templateBasename={result.templateBasename}
      />
    );
  }

  const page = parsePageParam(pageParam);
  const monthSlug = month.toString().padStart(2, '0');
  const daySlug = day.toString().padStart(2, '0');
  const result = await renderArchiveBundle({
    type: 'date',
    heading: formatDateHeading(year, month, day),
    basePath: `/${year}/${monthSlug}/${daySlug}`,
    page,
    year,
    month,
    day,
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
