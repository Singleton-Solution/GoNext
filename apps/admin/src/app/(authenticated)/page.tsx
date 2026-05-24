/**
 * Dashboard — landing page of the admin app.
 *
 * For the scaffold we render placeholder widgets that map onto the steady-state
 * dashboard from docs/05-admin-information-architecture.md §3. Each widget gets
 * its real data binding in a follow-up issue.
 */
import type { ReactElement } from 'react';

export default function DashboardPage(): ReactElement {
  return (
    <section>
      <h1>Dashboard</h1>
      <p className="muted">
        Welcome to the GoNext admin. This is the steady-state landing page;
        widgets below will be wired to live data in subsequent issues.
      </p>

      <div className="widget-grid">
        <article className="widget" aria-label="At a glance">
          <h2 className="widget__title">At a glance</h2>
          <p className="widget__body">
            Quick counts of published content, drafts, and users.
          </p>
        </article>

        <article className="widget" aria-label="Activity">
          <h2 className="widget__title">Activity</h2>
          <p className="widget__body">
            Recent edits, publishes, and comments.
          </p>
        </article>

        <article className="widget" aria-label="Quick draft">
          <h2 className="widget__title">Quick draft</h2>
          <p className="widget__body">
            Start a new post without leaving the dashboard.
          </p>
        </article>

        <article className="widget" aria-label="News">
          <h2 className="widget__title">News</h2>
          <p className="widget__body">
            Release notes and project updates from the GoNext team.
          </p>
        </article>
      </div>
    </section>
  );
}
