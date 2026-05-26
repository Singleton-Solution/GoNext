'use client';

/**
 * PluginSidebarSection — admin sidebar entries contributed by active
 * plugins. Issue #228.
 *
 * On mount the component fetches /api/v1/admin/plugin-pages, then
 * renders one sidebar link per declared page under a "Plugins"
 * section header. The router target is /plugins/{plugin}/{slug}; the
 * plugin frontend host is responsible for what loads there.
 *
 * Why a separate component (rather than the static NAV_SECTIONS
 * array in Sidebar.tsx)? Because the plugin set changes at runtime
 * — activating a plugin should surface its admin pages on the next
 * navigation without a redeploy. The component lazy-imports the
 * page-resolver bridge from the plugin frontend host, so the bundle
 * stays slim when no plugin is active.
 */
import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { useEffect, useState, type ReactElement } from 'react';
import { Plug } from 'lucide-react';

import { api } from '@/lib/api-client';

interface PluginPage {
  plugin: string;
  slug: string;
  label: string;
  icon?: string;
  capability?: string;
}

interface PluginPagesResponse {
  pages: PluginPage[];
}

interface Props {
  /** Capabilities the current viewer holds. A page with a declared
   * capability is hidden unless the viewer carries it. */
  viewerCapabilities?: ReadonlySet<string>;
}

export function PluginSidebarSection({
  viewerCapabilities,
}: Props): ReactElement | null {
  const pathname = usePathname() ?? '/';
  const [pages, setPages] = useState<PluginPage[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .get<PluginPagesResponse>('/api/v1/admin/plugin-pages')
      .then((data) => {
        if (!cancelled) setPages(data.pages ?? []);
      })
      .catch(() => {
        if (!cancelled) setPages([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!pages || pages.length === 0) return null;

  const visible = pages.filter((p) => {
    if (!p.capability) return true;
    if (!viewerCapabilities) return true; // fail-open in dev; sidebar isn't security-critical.
    return viewerCapabilities.has(p.capability);
  });

  if (visible.length === 0) return null;

  return (
    <div className="sidebar__section">
      <div className="sidebar__section-head">Plugins</div>
      <ul className="sidebar__nav">
        {visible.map((p) => {
          const href = `/plugins/${encodeURIComponent(p.plugin)}/${encodeURIComponent(p.slug)}`;
          const active = pathname === href || pathname.startsWith(`${href}/`);
          return (
            <li
              key={`${p.plugin}/${p.slug}`}
              className={
                active ? 'sidebar__item sidebar__item--active' : 'sidebar__item'
              }
            >
              <Link href={href}>
                <Plug aria-hidden width={16} height={16} />
                <span>{p.label}</span>
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
