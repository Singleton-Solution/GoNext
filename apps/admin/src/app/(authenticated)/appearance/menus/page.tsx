/**
 * Navigation menus admin — issue #54.
 *
 * Server entry that delegates to <MenusClient> for the live UX. The
 * server component does the initial GET against the admin API so the
 * first paint already carries the menus list; the client component
 * owns create / select / item-level CRUD with drag-to-reorder.
 */
import type { ReactElement } from 'react';
import { serverApiFetch } from '@/lib/server-api';
import { MenusClient } from './MenusClient';
import type { MenuListResponse } from './types';

export const dynamic = 'force-dynamic';

async function fetchInitial(): Promise<MenuListResponse | null> {
  try {
    const res = await serverApiFetch('/api/v1/admin/menus');
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
