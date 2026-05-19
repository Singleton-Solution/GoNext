/**
 * Section sidebar — renders the nav tree built by `lib/content.ts`.
 *
 * Server component: the nav tree is read at build time, no client state.
 * Active-link styling is intentionally light (a CSS attribute selector on
 * `[data-active="true"]`) so we don't need a client wrapper just for the
 * current-path comparison; the page route passes `activeSlug` in.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import type { NavNode, Section } from '@/lib/content';

interface SidebarProps {
  section: Section;
  nodes: NavNode[];
  /** Slug of the currently rendered page; matches `NavNode.slug`. */
  activeSlug?: string;
}

function NodeView({ section, node, activeSlug, depth }: { section: Section; node: NavNode; activeSlug?: string; depth: number }): ReactElement {
  if (node.slug !== undefined) {
    const href = node.slug === '' ? `/${section}` : `/${section}/${node.slug}`;
    const active = activeSlug === node.slug;
    return (
      <li className="sidebar__item" style={{ paddingLeft: `${depth * 12}px` }}>
        <Link href={href} data-active={active ? 'true' : 'false'} className="sidebar__link">
          {node.title}
        </Link>
      </li>
    );
  }
  return (
    <li className="sidebar__group" style={{ paddingLeft: `${depth * 12}px` }}>
      <div className="sidebar__group-title">{node.title}</div>
      {node.children && (
        <ul className="sidebar__list">
          {node.children.map((c, idx) => (
            <NodeView key={`${c.title}-${idx}`} section={section} node={c} activeSlug={activeSlug} depth={depth + 1} />
          ))}
        </ul>
      )}
    </li>
  );
}

export function Sidebar({ section, nodes, activeSlug }: SidebarProps): ReactElement {
  return (
    <aside className="sidebar" aria-label={`${section} navigation`}>
      <div className="sidebar__heading">{section === 'docs' ? 'Documentation' : 'Architecture Decisions'}</div>
      <ul className="sidebar__list">
        {nodes.map((n, idx) => (
          <NodeView key={`${n.title}-${idx}`} section={section} node={n} activeSlug={activeSlug} depth={0} />
        ))}
      </ul>
    </aside>
  );
}
