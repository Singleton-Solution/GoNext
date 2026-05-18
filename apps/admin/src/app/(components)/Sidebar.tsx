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
 */
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useState, type ReactElement } from 'react';

type NavItem = {
  href: string;
  label: string;
  icon: string;
};

const NAV_ITEMS: readonly NavItem[] = [
  { href: '/', label: 'Dashboard', icon: 'D' },
  { href: '/posts', label: 'Posts', icon: 'P' },
  { href: '/pages', label: 'Pages', icon: 'Pg' },
  { href: '/comments', label: 'Comments', icon: 'C' },
  { href: '/media', label: 'Media', icon: 'M' },
  { href: '/users', label: 'Users', icon: 'U' },
  { href: '/plugins', label: 'Plugins', icon: 'Pl' },
  { href: '/settings', label: 'Settings', icon: 'S' },
  // System Status is the operator surface (issue #221). It sits at the
  // bottom of the IA so it doesn't compete with the content-authoring
  // sections that occupy the rest of the sidebar. Access is gated
  // server-side via the system_read capability; the link itself is
  // visible to every signed-in user (a non-admin clicking through gets
  // a 403 on the API call and the page renders the error state).
  { href: '/status', label: 'System Status', icon: 'St' },
];

function isActive(currentPath: string, href: string): boolean {
  if (href === '/') {
    return currentPath === '/';
  }
  return currentPath === href || currentPath.startsWith(`${href}/`);
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
        <span className="sidebar__label">GoNext</span>
        <button
          type="button"
          className="sidebar__toggle"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed((v) => !v)}
        >
          {collapsed ? '>' : '<'}
        </button>
      </div>
      <nav>
        <ul className="sidebar__nav">
          {NAV_ITEMS.map((item) => {
            const active = isActive(pathname, item.href);
            return (
              <li
                key={item.href}
                className={
                  active ? 'sidebar__item sidebar__item--active' : 'sidebar__item'
                }
              >
                <Link href={item.href} aria-current={active ? 'page' : undefined}>
                  <span className="sidebar__icon" aria-hidden="true">
                    {item.icon}
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
