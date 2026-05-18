/**
 * System Status route entry.
 *
 * Server component that simply renders the client `StatusPage`. We
 * keep this thin shell so the App Router has a server component to
 * route to while all the interactive bits (fetch, refresh, clipboard)
 * live in the client component beneath it.
 */
import type { ReactElement } from 'react';
import { StatusPage } from './StatusPage';

export const dynamic = 'force-dynamic';

export default function SystemStatusRoute(): ReactElement {
  return <StatusPage />;
}
