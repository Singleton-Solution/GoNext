/**
 * "Header — logo + nav" pattern.
 *
 * A two-column header: brand on the left, primary navigation on the
 * right. We use a `core/group` wrapper with `tagName: header` so the
 * pattern produces a true `<header>` landmark; the inner columns lay
 * the brand and nav out horizontally without forcing the theme to
 * adopt a specific flexbox utility.
 *
 * The "navigation" itself is rendered as a `core/list` for now — the
 * dedicated navigation block lands alongside the menus issue, at which
 * point this pattern updates in place.
 */
import type { Pattern } from '../types.ts';

export const headerLogoNav: Pattern = {
  id: 'core/header-logo-nav',
  name: 'Header with logo and nav',
  category: 'header',
  description:
    'Two-column header with brand on the left and primary navigation on the right.',
  keywords: ['header', 'navigation', 'nav', 'menu', 'logo'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'header', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/columns',
          attributes: { columns: 2, isStackedOnMobile: false },
          innerBlocks: [
            {
              type: 'core/group',
              attributes: { tagName: 'div', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/heading',
                  attributes: { content: 'Acme', level: 1 },
                },
              ],
            },
            {
              type: 'core/group',
              attributes: { tagName: 'div', layout: 'flex' },
              innerBlocks: [
                {
                  type: 'core/list',
                  attributes: {
                    ordered: false,
                    values: ['Home', 'Features', 'Pricing', 'Blog', 'Contact'],
                  },
                },
              ],
            },
          ],
        },
      ],
    },
  ],
};
