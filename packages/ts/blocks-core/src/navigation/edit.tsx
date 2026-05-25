/**
 * `core/navigation` Edit component.
 *
 * Renders the link list inline alongside a working hamburger toggle so
 * authors see the same expand/collapse behaviour the published page will
 * produce. The toggle's expanded state is local component state — it
 * isn't persisted to attributes (the runtime toggle on the public site
 * is its own component) but mirroring it here matches the WP block's
 * behaviour and lets keyboard users tab through every menu item.
 *
 * Sub-menus are visualised but not interactively expanded from the
 * editor (one level of nesting only); deeper structures should compose
 * multiple Navigation blocks instead.
 *
 * The component renders exactly the same class names as `save()` so the
 * inspector's "preview at runtime size" inset has nothing to swap.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { useState, type ReactElement } from 'react';
import {
  DEFAULT_NAV_ARIA_LABEL,
  type NavigationAttributes,
  type NavigationItem,
} from './save.ts';

function NavLink({
  item,
}: {
  item: NavigationItem;
}): ReactElement {
  if (!item.url) {
    return (
      <span className="gn-block-navigation__item-label">{item.label}</span>
    );
  }
  return (
    <a href={item.url} rel={item.rel} target={item.target}>
      {item.label}
    </a>
  );
}

function NavItem({
  item,
}: {
  item: NavigationItem;
}): ReactElement {
  const children = item.children ?? [];
  if (children.length === 0) {
    return (
      <li className="gn-block-navigation__item">
        <NavLink item={item} />
      </li>
    );
  }
  return (
    <li className="gn-block-navigation__item has-submenu">
      <NavLink item={item} />
      <ul className="gn-block-navigation__submenu">
        {children.map((child, idx) => (
          <NavItem key={`${child.label}-${idx}`} item={child} />
        ))}
      </ul>
    </li>
  );
}

export function NavigationEdit({
  attributes,
  isSelected,
}: BlockEditProps<NavigationAttributes>): ReactElement {
  const items: NavigationItem[] = attributes.items ?? [];
  const orientation =
    attributes.orientation === 'vertical' ? 'vertical' : 'horizontal';

  // Local-only toggle state — the rendered <nav> on the public site
  // wires its own behaviour. Storing this in attrs would cause stale
  // persisted state on the next page load.
  const [expanded, setExpanded] = useState(false);

  const className = [
    'wp-block-navigation',
    'gn-block-navigation',
    `is-orientation-${orientation}`,
    attributes.hideToggle ? 'has-no-toggle' : null,
    isSelected ? 'is-selected' : null,
    expanded ? 'is-toggle-open' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  const aria = attributes.ariaLabel ?? DEFAULT_NAV_ARIA_LABEL;
  const menuId = 'gn-nav-menu-edit';

  return (
    <nav
      className={className}
      data-block="core/navigation"
      aria-label={aria}
    >
      {attributes.hideToggle ? null : (
        <button
          type="button"
          className="gn-block-navigation__toggle"
          aria-controls={menuId}
          aria-expanded={expanded}
          onClick={() => setExpanded((prev) => !prev)}
        >
          <span className="gn-block-navigation__toggle-bar" aria-hidden="true" />
          <span className="gn-block-navigation__toggle-bar" aria-hidden="true" />
          <span className="gn-block-navigation__toggle-bar" aria-hidden="true" />
          <span className="screen-reader-text">Menu</span>
        </button>
      )}
      <ul id={menuId} className="gn-block-navigation__container">
        {items.length === 0 ? (
          <li className="gn-block-navigation__item gn-block-navigation__placeholder">
            <span className="gn-block-navigation__item-label">
              Add menu items
            </span>
          </li>
        ) : (
          items.map((item, idx) => (
            <NavItem key={`${item.label}-${idx}`} item={item} />
          ))
        )}
      </ul>
    </nav>
  );
}

export default NavigationEdit;
