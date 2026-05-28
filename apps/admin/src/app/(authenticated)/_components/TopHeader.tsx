/**
 * TopHeader — the 54px chrome bar above every authenticated surface.
 *
 * Carries the GoNext wordmark on cream paper, a contextual page
 * label, and a small action cluster (notifications, "view site"
 * external link). The bar separates the forest-dark sidebar from
 * the page content via a 1px paper-border hairline — matching the
 * topbar treatment in `docs/design/ui_kits/admin/index.html`.
 *
 * The wordmark is rendered inline so the italic-accent move
 * (Archivo "Go" + Instrument-Serif "Next") stays in sync with the
 * brand tokens without a second SVG asset.
 *
 * Cosmetic-only at this stage: notifications + view-site are
 * placeholder anchors. Real wiring lands once the notifications
 * surface ships (issue #133).
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { Bell, ExternalLink } from 'lucide-react';

/**
 * Resolve the public-site URL for the "View site" link. NEXT_PUBLIC_SITE_URL
 * is the operator-set canonical front-end origin (production: the marketing
 * site). When unset we fall back to the docker-compose web service on
 * port 3000 — operators running the local stack expect that to open the
 * @gonext/web preview, not the admin's own root.
 *
 * The constant is evaluated at module-load time so the link href is
 * deterministic across renders.
 */
const SITE_URL =
  process.env.NEXT_PUBLIC_SITE_URL && process.env.NEXT_PUBLIC_SITE_URL !== ''
    ? process.env.NEXT_PUBLIC_SITE_URL
    : 'http://localhost:3000';

export function TopHeader(): ReactElement {
  return (
    <header className="app-shell__header" role="banner">
      <Link href="/" className="app-shell__brand" aria-label="GoNext admin home">
        <span className="app-shell__brand-go">Go</span>
        <span className="app-shell__brand-next">Next</span>
        <span className="app-shell__brand-tag">Admin</span>
      </Link>
      <div className="app-shell__header-actions">
        <a
          href={SITE_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="app-shell__view-site"
          aria-label="View site (opens in new tab)"
        >
          <ExternalLink aria-hidden="true" width={13} height={13} />
          View site
        </a>
        <button
          type="button"
          className="app-shell__icon-btn"
          aria-label="Notifications"
        >
          <Bell aria-hidden="true" width={16} height={16} />
          <span className="app-shell__icon-badge" aria-hidden="true" />
        </button>
      </div>
    </header>
  );
}
