'use client';

/**
 * Sidebar — primary navigation, brand "Living systems" treatment.
 *
 * Replaces the scaffold sidebar with the forest-dark surface from
 * `docs/design/ui_kits/admin/index.html`:
 *
 *   - 248px wide forest (#0E1A14) surface, forest-border hairlines.
 *   - Org-switch chip at the top (logo-mark + workspace name + plan
 *     dot). Cosmetic for now; the workspace switcher lands in its
 *     own issue.
 *   - Grouped nav with section heads (Workspace · Content · Studio).
 *     Lucide icons render in emerald-bright when the section is
 *     active.
 *   - "Grow to Agency" upgrade card with the brand's organic-glow
 *     radial gradient and emerald CTA. Static copy at this stage;
 *     once the billing surface lands it can become dismissable.
 *   - Side-foot user chip: emerald avatar (display-type initials),
 *     name + email, log-out trigger.
 *
 * The visible nav set is the canonical IA from
 * `docs/05-admin-information-architecture.md`. Pages that the
 * scaffold doesn't yet ship still point at their target route; the
 * target page just renders the placeholder until the feature lands.
 *
 * Auth-related behaviour (collapsing, search) is preserved from the
 * pre-restyle sidebar — collapsed state is local for now, GlobalSearch
 * still mounts under the org switcher.
 */
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  useState,
  type ComponentType,
  type ReactElement,
  type SVGProps,
} from 'react';
import {
  Activity,
  Bell,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  CornerDownRight,
  ExternalLink,
  FileText,
  Gauge,
  ImageIcon,
  Layout,
  LineChart,
  LogOut,
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

interface NavItem {
  href: string;
  label: string;
  Icon: LucideIcon;
  /** Right-aligned count badge (cosmetic for now — wired in a follow-up). */
  count?: string;
}

interface NavSection {
  head: string;
  items: readonly NavItem[];
}

/**
 * Grouped IA. Section heads mirror the ui_kit prototype:
 *   • Workspace — overview + pulse (the brand's analytics surface).
 *   • Content   — posts, pages, media, comments, users (CRUD).
 *   • Studio    — appearance, customizer, marketplace, plugins,
 *                 redirects, search, settings + operator surfaces
 *                 (status, performance).
 * Counts are cosmetic placeholders until the real aggregate endpoint
 * lands — see issue #76.
 */
const NAV_SECTIONS: readonly NavSection[] = [
  {
    head: 'Workspace',
    items: [
      { href: '/', label: 'Dashboard', Icon: Gauge },
    ],
  },
  {
    head: 'Content',
    items: [
      { href: '/posts', label: 'Posts', Icon: FileText },
      { href: '/pages', label: 'Pages', Icon: Layout },
      { href: '/comments', label: 'Comments', Icon: MessageSquare },
      { href: '/media', label: 'Media', Icon: ImageIcon },
      { href: '/users', label: 'Users', Icon: Users },
    ],
  },
  {
    head: 'Studio',
    items: [
      { href: '/appearance/themes', label: 'Appearance', Icon: Palette },
      { href: '/appearance/customizer', label: 'Customize', Icon: Sliders },
      { href: '/marketplace', label: 'Marketplace', Icon: Store },
      { href: '/plugins', label: 'Plugins', Icon: Plug },
      { href: '/redirects', label: 'Redirects', Icon: CornerDownRight },
      { href: '/search', label: 'Search', Icon: Search },
      { href: '/settings', label: 'Settings', Icon: Settings },
    ],
  },
  {
    head: 'Operator',
    items: [
      { href: '/status', label: 'System Status', Icon: Activity },
      { href: '/performance', label: 'Performance', Icon: LineChart },
    ],
  },
];

function isActive(currentPath: string, href: string): boolean {
  if (href === '/') {
    return currentPath === '/';
  }
  return currentPath === href || currentPath.startsWith(`${href}/`);
}

/**
 * Inline-renders the GoNext wordmark instead of pulling it via
 * <img src="/logo-wordmark.svg"> so the mark inherits `currentColor`
 * for paper-on-forest contrast without shipping a second asset just
 * for the dark sidebar.
 */
function WordmarkLogo(): ReactElement {
  return (
    <span className="sidebar__wordmark" aria-label="GoNext">
      <span className="sidebar__wm-go">Go</span>
      <span className="sidebar__wm-next">Next</span>
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
      {/* Org switcher — workspace + plan dot. Collapse toggle sits
          to the right so the sidebar can fold to icon-only without
          losing access to the workspace switcher. */}
      <div className="sidebar__org" role="presentation">
        <span className="sidebar__org-mark" aria-hidden="true">
          <WordmarkLogo />
        </span>
        {!collapsed && (
          <>
            <span className="sidebar__org-info">
              <span className="sidebar__org-name">Your workspace</span>
              <span className="sidebar__org-plan">Studio plan</span>
            </span>
            <ChevronsUpDown
              aria-hidden="true"
              className="sidebar__org-chev"
              width={14}
              height={14}
            />
          </>
        )}
        <button
          type="button"
          className="sidebar__toggle"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed((v) => !v)}
        >
          {collapsed ? (
            <ChevronRight aria-hidden="true" width={12} height={12} />
          ) : (
            <ChevronLeft aria-hidden="true" width={12} height={12} />
          )}
        </button>
      </div>

      {!collapsed && (
        <div className="sidebar__search">
          <GlobalSearch />
        </div>
      )}

      <nav className="sidebar__nav-wrap">
        {NAV_SECTIONS.map((section) => (
          <div key={section.head} className="sidebar__section">
            <div className="sidebar__section-head">{section.head}</div>
            <ul className="sidebar__nav">
              {section.items.map((item) => {
                const active = isActive(pathname, item.href);
                const { Icon } = item;
                return (
                  <li
                    key={item.href}
                    className={
                      active
                        ? 'sidebar__item sidebar__item--active'
                        : 'sidebar__item'
                    }
                  >
                    <Link
                      href={item.href}
                      aria-current={active ? 'page' : undefined}
                    >
                      <span className="sidebar__icon" aria-hidden="true">
                        <Icon width={15} height={15} strokeWidth={1.75} />
                      </span>
                      <span className="sidebar__label">{item.label}</span>
                      {item.count ? (
                        <span className="sidebar__count">{item.count}</span>
                      ) : null}
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </nav>

      {/* Upgrade card — radial-glow forest-2 surface, emerald CTA. */}
      <div className="sidebar__upgrade">
        <div className="sidebar__upgrade-title">
          Grow to <em>Agency</em>
        </div>
        <p className="sidebar__upgrade-body">
          Unlimited sites, SSO, audit logs, and a lower commerce fee.
        </p>
        <Link href="/settings" className="sidebar__upgrade-cta">
          Compare plans
        </Link>
      </div>

      {/* User foot — emerald initials avatar + sign-out. */}
      <div className="sidebar__foot">
        <span className="sidebar__avatar" aria-hidden="true">
          GN
        </span>
        <span className="sidebar__who">
          <span className="sidebar__who-name">Signed in</span>
          <span className="sidebar__who-email">admin@workspace</span>
        </span>
        <Link
          href="/api/v1/auth/logout"
          aria-label="Sign out"
          className="sidebar__signout"
        >
          <LogOut aria-hidden="true" width={14} height={14} />
        </Link>
      </div>
    </aside>
  );
}

/** Re-export the header bell + view-site action shapes the header uses. */
export { Bell as HeaderBell, ExternalLink as HeaderExternal };
