/**
 * Navigation menus admin — issue #54.
 *
 * Server entry that delegates to <MenusClient> for the live UX. The
 * server component does the initial GET against the admin API so the
 * first paint already carries the menus list; the client component
 * owns create / select / item-level CRUD with drag-to-reorder.
 */
import type { ReactElement } from 'react';
import { cookies } from 'next/headers';
import { apiBaseUrl } from '@/lib/api-client';
import { MenusClient } from './MenusClient';
import type { MenuListResponse } from './types';

export const dynamic = 'force-dynamic';

async function fetchInitial(): Promise<MenuListResponse | null> {
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
  try {
    const res = await fetch(`${apiBaseUrl}/api/v1/admin/menus`, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      cache: 'no-store',
    });
    if (!res.ok) return null;
    return (await res.json()) as MenuListResponse;
  } catch {
    return null;
  }
}

export default async function MenusPage(): Promise<ReactElement> {
  const data = await fetchInitial();
  return <MenusClient initialMenus={data?.menus ?? []} />;
}
