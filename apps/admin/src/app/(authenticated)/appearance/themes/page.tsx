/**
 * Themes umbrella page — `/appearance/themes` (issues #13, #18, #65).
 *
 * This route combines the theme switcher (issue #18) and the .gntheme
 * installer (issue #13) into a single surface so operators only have
 * one place to install + activate themes. The server component does
 * the initial fetch against `GET /api/v1/admin/themes`, forwarding
 * the inbound session cookie so the API auth middleware accepts the
 * call; the client component owns the optimistic activate flow and
 * the drag-drop upload form.
 *
 * The page deliberately renders even when the API list call errors
 * (empty themes array + empty active slug). The drop zone is what
 * the operator wants in that state anyway — install the first theme,
 * the next render hydrates the gallery.
 */

import type { ReactElement } from 'react';
import { cookies } from 'next/headers';
import { fetchThemesList } from './api';
import { ThemesGalleryClient } from './ThemesGalleryClient';

export const dynamic = 'force-dynamic';

export default async function ThemesPage(): Promise<ReactElement> {
  let cookieHeader = '';
  try {
    const store = await cookies();
    cookieHeader = store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    cookieHeader = '';
  }
  const data = await fetchThemesList(cookieHeader);
  return (
    <ThemesGalleryClient
      initialThemes={data?.themes ?? []}
      initialActiveSlug={data?.active_slug ?? ''}
    />
  );
}
