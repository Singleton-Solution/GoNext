/**
 * Homepage handler — `/` route.
 *
 * Renders the blog-home (latest posts) feed by default. Once site
 * settings let an admin pin a static front page, the handler reads
 * the `core.reading.show_on_front` setting and dispatches between
 * `renderArchive({ type: 'home' })` and
 * `renderSingular(<frontPageSlug>)` accordingly. Until that wiring
 * lands the default behaviour is the same as classic WP: latest
 * posts.
 *
 * The catch-all slug route is owned by `[...slug]/page.tsx`; Next
 * routes `/` here because root-level `page.tsx` wins over the
 * catch-all for the empty slug array.
 */
import { cookies } from 'next/headers';
import type { ReactElement } from 'react';
import { renderArchive } from '@/lib/render';
import { PublicShell } from './PublicShell';

export const dynamic = 'force-dynamic';

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

export default async function HomePage(): Promise<ReactElement> {
  const cookie = await readCookieHeader();
  const result = await renderArchive({
    cookie,
    type: 'home',
    heading: 'Latest posts',
  });
  return (
    <PublicShell
      bodyHtml={result.html}
      cssCustomProperties={result.css}
      templateBasename={result.templateBasename}
    />
  );
}
