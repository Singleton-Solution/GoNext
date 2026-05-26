/**
 * Active-sessions self-service — issue #205.
 *
 * Lists the current user's live sessions (GET /api/v1/auth/sessions),
 * lets them revoke any single session, and exposes a "Sign out of all
 * other devices" bulk action. The route is gated by the authenticated
 * layout — anonymous traffic gets redirected at the edge.
 */
import type { ReactElement } from 'react';
import { SessionsClient } from './SessionsClient';

export const dynamic = 'force-dynamic';

export default function SessionsPage(): ReactElement {
  return <SessionsClient />;
}
