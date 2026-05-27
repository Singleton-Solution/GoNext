/**
 * /setup — first-run install wizard route.
 *
 * The page is a server component that fetches the install status before
 * rendering. Three outcomes:
 *
 *   1. `installation_completed: true` → redirect to /login. The install
 *      window has closed; the operator (or whoever opened the tab) is
 *      now an ordinary user.
 *   2. Status fetch failed → render an error card with a "Try again"
 *      hint. The wizard cannot proceed without knowing the server's
 *      lock state.
 *   3. Otherwise → render <SetupWizard /> seeded with the status payload.
 *
 * The page deliberately runs `force-dynamic` so the install lock check
 * is consulted on every navigation (no ISR cache, no static page). On a
 * fresh deploy this surface is hit at most a handful of times before
 * the lock seals it; after that it 302s, so caching it would be a
 * pessimization for both correctness and operator experience.
 */
import type { ReactElement } from 'react';
import { redirect } from 'next/navigation';
import { serverApiGet } from '@/lib/server-api';
import SetupWizard from './SetupWizard';
import type { SetupStatus } from './types';

// Tells Next.js to opt out of static generation. The install lock can
// flip at any moment (the API server returns 423 once the marker is
// set), and a stale 200 would leave us showing the wizard after it
// should have closed.
export const dynamic = 'force-dynamic';

/**
 * Server-side fetch of the install status. Returns either the parsed
 * payload or null when the API is unreachable / responds non-200.
 *
 * Uses `serverApiGet` for a consistent code path with the rest of the
 * admin's server-side fetches — no cookie is expected on `/setup`
 * (the user isn't signed in yet) but the helper happily forwards an
 * empty cookie store and the API ignores it.
 */
async function fetchStatus(): Promise<SetupStatus | null> {
  try {
    const json = await serverApiGet<Partial<SetupStatus>>(
      '/api/v1/setup/status',
    );
    if (typeof json.installation_completed !== 'boolean') return null;
    if (typeof json.user_count !== 'number') return null;
    return {
      installation_completed: json.installation_completed,
      user_count: json.user_count,
    };
  } catch {
    return null;
  }
}

export default async function SetupPage(): Promise<ReactElement> {
  const status = await fetchStatus();

  if (status === null) {
    return (
      <section className="setup">
        <header className="setup__header">
          <h1>Set up GoNext</h1>
        </header>
        <div className="setup-step">
          <h2>Cannot reach the API</h2>
          <p>
            The setup wizard needs to reach{' '}
            <code>/api/v1/setup/status</code> to determine whether GoNext
            is already installed. Verify the API container is running
            and reachable from the admin pod, then reload this page.
          </p>
        </div>
      </section>
    );
  }

  if (status.installation_completed) {
    // The install window has already closed. Send the operator to login
    // — the same fallback the middleware would produce on any other
    // admin URL.
    redirect('/login');
  }

  return <SetupWizard initialStatus={status} />;
}
