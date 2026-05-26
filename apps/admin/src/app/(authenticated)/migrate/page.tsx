/**
 * Migration wizard — server entry.
 *
 * The wizard walks an operator through a one-time WordPress → GoNext
 * migration in five steps:
 *
 *   1. Source — pick a WXR upload, a live WP REST URL, or a path to
 *      an ACF JSON export.
 *   2. Options — media mode (copy/proxy), role overrides, shortcode
 *      handling.
 *   3. Dry-run preview — counts of posts/media/users that will land.
 *   4. Run + progress — kicks off the import; polls a job id from
 *      the API.
 *   5. Report — terminal counts, per-record errors, and a link to the
 *      plugin replacement guide (#230) for unmigrated surfaces.
 *
 * The server boundary here exists only so a future "resume this
 * in-progress job" pre-flight (see issue #234 comments) can fetch the
 * latest job for the operator before the client takes over. Today the
 * wizard starts cold every time.
 *
 * Issue: #234.
 */
import type { ReactElement } from 'react';
import { Headline } from '@/components/ui/headline';
import { MigrationWizard } from './MigrationWizard';

export const dynamic = 'force-dynamic';

export default function MigratePage(): ReactElement {
  return (
    <div className="px-6 py-6">
      <header className="mb-6">
        <Headline as="h1">
          WordPress <em>migration</em>
        </Headline>
        <p className="text-fg-muted mt-2">
          Walk a one-shot import from a WordPress export. Five steps,
          dry-run preview before commit.
        </p>
      </header>
      <MigrationWizard />
    </div>
  );
}
