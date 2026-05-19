/**
 * Site Editor Lite — route entry.
 *
 * Server component that renders the client island below. Force-dynamic
 * because the underlying API call needs the operator's session cookie
 * and we don't want the Next cache layer holding stale parts content.
 */
import type { ReactElement } from 'react';
import { SiteEditorClient } from './SiteEditorClient';

export const dynamic = 'force-dynamic';

export default function SiteEditorPage(): ReactElement {
  return <SiteEditorClient />;
}
