/**
 * "Footer — four columns" pattern.
 *
 * Standard site-wide footer: four columns of links plus a separator and
 * a copyright line. The outer wrapper is `<footer>` so the rendered
 * page has a proper landmark; the columns stack on mobile via the
 * core columns block's built-in behaviour.
 */
import type { Pattern } from '../types.ts';

export const footerMultiColumn: Pattern = {
  id: 'core/footer-multi-column',
  name: 'Four-column footer',
  category: 'footer',
  description:
    'A semantic `<footer>` with four link columns, a separator, and a copyright line.',
  keywords: ['footer', 'sitemap', 'links', 'colophon'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'footer', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/columns',
          attributes: { columns: 4, isStackedOnMobile: true },
          innerBlocks: [
            {
              type: 'core/group',
              attributes: { tagName: 'div', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/heading',
                  attributes: { content: 'Product', level: 4 },
                },
                {
                  type: 'core/list',
                  attributes: {
                    ordered: false,
                    values: ['Features', 'Pricing', 'Roadmap', 'Changelog'],
                  },
                },
              ],
            },
            {
              type: 'core/group',
              attributes: { tagName: 'div', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/heading',
                  attributes: { content: 'Company', level: 4 },
                },
                {
                  type: 'core/list',
                  attributes: {
                    ordered: false,
                    values: ['About', 'Blog', 'Careers', 'Press'],
                  },
                },
              ],
            },
            {
              type: 'core/group',
              attributes: { tagName: 'div', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/heading',
                  attributes: { content: 'Resources', level: 4 },
                },
                {
                  type: 'core/list',
                  attributes: {
                    ordered: false,
                    values: ['Docs', 'Guides', 'API', 'Community'],
                  },
                },
              ],
            },
            {
              type: 'core/group',
              attributes: { tagName: 'div', layout: 'default' },
              innerBlocks: [
                {
                  type: 'core/heading',
                  attributes: { content: 'Legal', level: 4 },
                },
                {
                  type: 'core/list',
                  attributes: {
                    ordered: false,
                    values: ['Privacy', 'Terms', 'Security', 'DPA'],
                  },
                },
              ],
            },
          ],
        },
        {
          type: 'core/separator',
          attributes: { style: 'default' },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content: 'Copyright (c) Acme, Inc. All rights reserved.',
            align: 'center',
          },
        },
      ],
    },
  ],
};
