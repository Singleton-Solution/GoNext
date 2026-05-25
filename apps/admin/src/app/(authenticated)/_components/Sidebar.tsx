'use client';

/**
 * Sidebar — collapsible primary navigation for the admin shell.
 *
 * The link set matches the canonical admin information architecture
 * (see docs/05-admin-information-architecture.md). Sections whose feature
 * code has not yet landed (Pages, Comments, Media) link to "Coming soon"
 * placeholder routes so the nav stays visible and the IA is testable in
 * isolation from the feature rollout.
 *
 * The collapsed state is local to this component for the scaffold; once the
 * user-preferences store lands (issue #43) it should be lifted there so the
 * choice persists across sessions.
 *
 * Visual treatment follows docs/design/HANDOFF.md "admin dashboard"
 * shell: forest-ink sidebar surface, --r-md hairlines, Lucide icons
 * paired with Geist labels, and the GoNext wordmark logo in place of
 * the original "GoNext" plain-text label. The icon-rendering hooks
 * use `aria-hidden` so screen readers still see only the link's
 * label, preserving the accessible-name contract the existing tests
 * pin.
 */
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useState, type ComponentType, type ReactElement, type SVGProps } from 'react';
import {
  Activity,
  ChevronLeft,
  ChevronRight,
  CornerDownRight,
  FileText,
  Gauge,
  ImageIcon,
  Layout,
  LineChart,
  MessageSquare,
  Palette,
  Plug,
  Search,
  Settings,
  Sliders,
  Store,
  Users,
} from 'lucide-react';
import { GlobalSearch } from '../../../components/GlobalSearch';

type LucideIcon = ComponentType<SVGProps<SVGSVGElement>>;

type NavItem = {
  href: string;
  label: string;
  Icon: LucideIcon;
};

const NAV_ITEMS: readonly NavItem[] = [
  { href: '/', label: 'Dashboard', Icon: Gauge },
  { href: '/posts', label: 'Posts', Icon: FileText },
  { href: '/pages', label: 'Pages', Icon: Layout },
  { href: '/comments', label: 'Comments', Icon: MessageSquare },
  { href: '/media', label: 'Media', Icon: ImageIcon },
  { href: '/users', label: 'Users', Icon: Users },
  // Search is the full-page result view that the sidebar GlobalSearch
  // overlay routes to on Enter-without-focus. We expose it as a nav
  // entry so deep-linked /search?q=… URLs still slot into the IA;
  // most users will reach it through the always-visible search input.
  { href: '/search', label: 'Search', Icon: Search },
  { href: '/plugins', label: 'Plugins', Icon: Plug },
  // Marketplace is the browse + install entry point sibling to the
  // installed-plugins manager. Adminstrators land here to discover new
  // plugins; the install flow on this surface reuses the same
  // CapabilityReview component as the manual install path.
  { href: '/marketplace', label: 'Marketplace', Icon: Store },
  // Appearance → Site Editor surface (issue #428). The link points
  // at the lite cut today; v0.2 expands the same section with full
  // template editing.
  { href: '/appearance/site-editor', label: 'Appearance', Icon: Palette },
  // Appearance funnels into the Theme Customizer surface (#355). The
  // link points straight at /appearance/customizer because that's the
  // only landing inside the section today; an index page lands when
  // theme installation arrives.
  { href: '/appearance/customizer', label: 'Customize', Icon: Sliders },
  // Redirects is the WordPress-parity 301/302/307/308 admin surface.
  // Operators manage literal and regex rules; the API middleware
  // serves matched paths with the configured status BEFORE the
  // renderer sees them.
  { href: '/redirects', label: 'Redirects', Icon: CornerDownRight },
  { href: '/settings', label: 'Settings', Icon: Settings },
  // System Status is the operator surface (issue #221). It sits at the
  // bottom of the IA so it doesn't compete with the content-authoring
  // sections that occupy the rest of the sidebar. Access is gated
  // server-side via the system_read capability; the link itself is
  // visible to every signed-in user (a non-admin clicking through gets
  // a 403 on the API call and the page renders the error state).
  { href: '/status', label: 'System Status', Icon: Activity },
  // Performance is the Core Web Vitals operator surface fed by the
  // in-house RUM beacon (issue #132). Same access posture as System
  // Status: visible in the nav, server-side capability-gated.
  { href: '/performance', label: 'Performance', Icon: LineChart },
];

function isActive(currentPath: string, href: string): boolean {
  if (href === '/') {
    return currentPath === '/';
  }
  return currentPath === href || currentPath.startsWith(`${href}/`);
}

/**
 * Inline-renders the GoNext wordmark SVG instead of pulling it via
 * <img src="/logo-wordmark.svg"> so the mark inherits `currentColor`
 * for paper-on-forest contrast without shipping a second asset just
 * for the dark sidebar.
 */
function WordmarkLogo(): ReactElement {
  return (
    <span className="sidebar__wordmark" aria-label="GoNext">
      <span
        style={{
          fontFamily: 'var(--font-display)',
          fontWeight: 800,
          fontSize: '18px',
          letterSpacing: '-0.03em',
          color: 'var(--fg-on-forest)',
        }}
      >
        Go
      </span>
      <span
        style={{
          fontFamily: 'var(--font-serif)',
          fontWeight: 400,
          fontStyle: 'italic',
          fontSize: '20px',
          color: 'var(--fg-on-forest)',
          marginLeft: '1px',
        }}
      >
        Next
      </span>
    </span>
  );
}

export function Sidebar(): ReactElement {
  const pathname = usePathname() ?? '/';
  const [collapsed, setCollapsed] = useState(false);

  return (
    <aside
      className={collapsed ? 'sidebar sidebar--collapsed' : 'sidebar'}
      aria-label="Primary navigation"
    >
      <div className="sidebar__header">
        <span className="sidebar__label">
          <WordmarkLogo />
        </span>
        <button
          type="button"
          className="sidebar__toggle"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed((v) => !v)}
        >
          {collapsed ? (
            <ChevronRight aria-hidden="true" width={14} height={14} />
          ) : (
            <ChevronLeft aria-hidden="true" width={14} height={14} />
          )}
        </button>
      </div>
      {!collapsed && (
        <div className="sidebar__search">
          <GlobalSearch />
        </div>
      )}
      <nav>
        <ul className="sidebar__nav">
          {NAV_ITEMS.map((item) => {
            const active = isActive(pathname, item.href);
            const { Icon } = item;
            return (
              <li
                key={item.href}
                className={
                  active ? 'sidebar__item sidebar__item--active' : 'sidebar__item'
                }
              >
                <Link href={item.href} aria-current={active ? 'page' : undefined}>
                  <span className="sidebar__icon" aria-hidden="true">
                    <Icon width={16} height={16} strokeWidth={1.75} />
                  </span>
                  <span className="sidebar__label">{item.label}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>
    </aside>
  );
}
