/**
 * Pure helpers for the comments thread renderer.
 *
 * Building the tree on the client is a tradeoff:
 *  + Single fetch, no per-thread joins on the server.
 *  + The tree-shape state stays a derived value, not state to sync.
 *  - O(n) memory + O(n log n) sort on render. Fine up to thousands
 *    of comments; if a single post grows past that we'd want a
 *    server-rendered tree.
 *
 * The build function is exported separately from the React component
 * so the test file can drive it without a DOM.
 */
import type { PublicComment, ThreadNode } from './types';

/**
 * Build a tree of ThreadNodes from a flat list of comments.
 *
 * Orphans (parent_id refers to a comment not in the list — e.g. the
 * parent is still pending, or was trashed) are promoted to top-level
 * so the visitor still sees the row. The order of siblings follows
 * the ltree path, which embeds a v7 UUID — chronological by design.
 */
export function buildThread(comments: PublicComment[]): ThreadNode[] {
  const byId = new Map<string, ThreadNode>();
  for (const c of comments) {
    byId.set(c.id, { comment: c, children: [] });
  }

  const roots: ThreadNode[] = [];
  for (const c of comments) {
    const node = byId.get(c.id);
    if (!node) continue; // defensive — impossible by construction
    if (c.parent_id && byId.has(c.parent_id)) {
      const parent = byId.get(c.parent_id);
      if (parent) {
        parent.children.push(node);
        continue;
      }
    }
    roots.push(node);
  }

  // Sort by path so siblings appear in submission order.
  const sortByPath = (a: ThreadNode, b: ThreadNode): number =>
    a.comment.path < b.comment.path ? -1 : a.comment.path > b.comment.path ? 1 : 0;
  roots.sort(sortByPath);
  for (const node of byId.values()) {
    node.children.sort(sortByPath);
  }
  return roots;
}

/**
 * Read a cookie value from `document.cookie` by name.
 *
 * Returns the raw cookie value (URL-decoded). The CSRF cookie is the
 * primary consumer; we keep the helper generic so the form can
 * forward additional cookies (e.g. a session cookie) without growing
 * a per-cookie helper.
 */
export function readCookie(name: string, cookieString: string): string {
  if (!name || !cookieString) return '';
  const target = `${name}=`;
  for (const piece of cookieString.split(';')) {
    const trimmed = piece.trim();
    if (trimmed.startsWith(target)) {
      return decodeURIComponent(trimmed.slice(target.length));
    }
  }
  return '';
}

/**
 * Format a comment timestamp for display.
 *
 * Defaults to the visitor's locale; tests pin the locale to
 * "en-US" so the snapshot is deterministic.
 */
export function formatTimestamp(iso: string, locale?: string): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString(locale ?? undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}
