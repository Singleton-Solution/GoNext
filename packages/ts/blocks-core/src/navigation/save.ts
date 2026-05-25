/**
 * `core/navigation` save serializer + server-render hint.
 *
 * Navigation is a leaf block: it persists a flat array of `NavigationItem`
 * records (label + URL + optional rel/target/nested items) and renders
 * directly to `<nav>` markup. The block does NOT pull from `innerBlocks`
 * — keeping the menu data in attributes lets the same block represent
 * either an inline-authored list (the `items` attribute populated) or a
 * server-resolved menu (`menuId` set, server-render walks the DB).
 *
 * Mobile behaviour is handled progressively: the saved HTML always
 * includes a `<button class="gn-block-navigation__toggle" aria-controls>`
 * trigger paired with the menu list. Themes show/hide the toggle via
 * media queries; the editor-time hamburger toggle in `edit.tsx` mirrors
 * what the rendered output will do at runtime.
 *
 * The structure mirrors WP's `core/navigation` block markup so existing
 * themes targeting `.wp-block-navigation` keep styling. Class names are
 * additionally prefixed with `gn-block-navigation` for theme overrides
 * that want to target GoNext-specific behaviour without colliding.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** A single link in a navigation menu. */
export interface NavigationItem {
  /** Visible link label. Plain text — escaped at render time. */
  label: string;
  /**
   * Destination URL. Absolute or root-relative. Empty string is allowed
   * for placeholder items (the editor renders them as inert).
   */
  url: string;
  /** Optional `rel` attribute (e.g. `noopener noreferrer`). */
  rel?: string;
  /** Optional `target`. Pass `_blank` for new-tab links. */
  target?: '_self' | '_blank';
  /**
   * Optional sub-menu items rendered as a nested `<ul>`. One level deep —
   * deeper structures should compose multiple Navigation blocks.
   */
  children?: NavigationItem[];
}

/** Attribute shape for `core/navigation`. */
export interface NavigationAttributes extends BlockAttributes {
  /**
   * Inline menu items. Authoritative when set. The editor uses this for
   * "build a menu in this block"; server-render uses it as the fallback
   * when `menuId` is unset or its lookup fails.
   */
  items?: NavigationItem[];
  /**
   * Identifier of a server-stored menu. When set, server-render resolves
   * the items from the DB; the editor surfaces a read-only preview.
   * Validation accepts any non-empty string (UUIDs, slugs, integer ids).
   */
  menuId?: string;
  /**
   * Layout orientation. Horizontal is the default and what most themes
   * expect; vertical is useful for footer columns and sidebars.
   */
  orientation?: 'horizontal' | 'vertical';
  /**
   * When true, an accessible-label override applied to the rendered
   * `<nav aria-label="...">`. Falls back to a generic "Site navigation"
   * if omitted.
   */
  ariaLabel?: string;
  /**
   * When true, the toggle button is omitted entirely — useful for footer
   * menus that should always be visible.
   */
  hideToggle?: boolean;
}

/** Public default for the rendered `aria-label` fallback. */
export const DEFAULT_NAV_ARIA_LABEL = 'Site navigation';

function navClasses(attrs: NavigationAttributes): string[] {
  const orientation = attrs.orientation === 'vertical' ? 'vertical' : 'horizontal';
  return [
    'wp-block-navigation',
    'gn-block-navigation',
    `is-orientation-${orientation}`,
    attrs.hideToggle ? 'has-no-toggle' : null,
  ].filter((c): c is string => c !== null);
}

/** Render the `<a>` for a single item — empty URL collapses to a span. */
function renderLink(item: NavigationItem): string {
  if (!item.url) {
    return `<span class="gn-block-navigation__item-label">${escapeHtml(item.label)}</span>`;
  }
  const rel = item.rel ? ` rel="${escapeHtml(item.rel)}"` : '';
  const target = item.target ? ` target="${escapeHtml(item.target)}"` : '';
  return `<a href="${escapeHtml(item.url)}"${target}${rel}>${escapeHtml(item.label)}</a>`;
}

/** Render a single `<li>` — recurses one level into children. */
function renderItem(item: NavigationItem): string {
  const link = renderLink(item);
  if (item.children && item.children.length > 0) {
    const subItems = item.children.map(renderItem).join('');
    return `<li class="gn-block-navigation__item has-submenu">${link}<ul class="gn-block-navigation__submenu">${subItems}</ul></li>`;
  }
  return `<li class="gn-block-navigation__item">${link}</li>`;
}

/** Render the toggle + menu list as a single fragment. */
function renderMenu(attrs: NavigationAttributes, items: NavigationItem[]): string {
  const list = items.map(renderItem).join('');
  const menuId = 'gn-nav-menu';
  const toggle = attrs.hideToggle
    ? ''
    : `<button type="button" class="gn-block-navigation__toggle" aria-controls="${menuId}" aria-expanded="false"><span class="gn-block-navigation__toggle-bar" aria-hidden="true"></span><span class="gn-block-navigation__toggle-bar" aria-hidden="true"></span><span class="gn-block-navigation__toggle-bar" aria-hidden="true"></span><span class="screen-reader-text">Menu</span></button>`;
  return `${toggle}<ul id="${menuId}" class="gn-block-navigation__container">${list}</ul>`;
}

/**
 * Pure serializer. Always emits the `<nav>` wrapper so theme CSS gets a
 * stable target even when no items are configured (the rendered list is
 * empty but well-formed).
 */
export function save({
  attributes,
}: BlockSaveProps<NavigationAttributes>): string {
  const items = attributes.items ?? [];
  const aria = attributes.ariaLabel ?? DEFAULT_NAV_ARIA_LABEL;
  return `<nav${classAttr(navClasses(attributes))} aria-label="${escapeHtml(aria)}">${renderMenu(attributes, items)}</nav>`;
}

/**
 * Server-render hint. Mirrors `save()` byte-for-byte when only `items` is
 * set; when `menuId` is in play, the Go walker passes a server-resolved
 * list through `_innerHtml` (currently unused — items still flow through
 * attributes for now) and we'll route through the same templater. This
 * function's role today is the parity contract documented on `CoreBlock`.
 */
export function serverRender(
  attrs: NavigationAttributes,
  _innerHtml: string,
): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
