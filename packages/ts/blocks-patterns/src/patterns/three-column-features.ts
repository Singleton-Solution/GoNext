/**
 * "Three-column features" pattern.
 *
 * A `core/columns` parent with three feature cards — each a heading +
 * paragraph pair. The classic marketing-page "what we do" grid.
 *
 * Author note: the columns container forces a fixed three-column track,
 * but `isStackedOnMobile: true` ensures the cards collapse to a vertical
 * stack under the theme's mobile breakpoint without extra work from the
 * author.
 */
import type { Pattern } from '../types.ts';

export const threeColumnFeatures: Pattern = {
  id: 'core/three-column-features',
  name: 'Three-column features',
  category: 'features',
  description:
    'Three side-by-side feature cards with headings and supporting copy.',
  keywords: ['features', 'grid', 'columns', 'services'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/columns',
      attributes: {
        columns: 3,
        isStackedOnMobile: true,
      },
      innerBlocks: [
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: 'Fast', level: 3 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Server-rendered pages with edge-cached assets keep your site quick by default.',
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
              attributes: { content: 'Flexible', level: 3 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Compose pages from blocks, then extend them with plugins when you outgrow the core set.',
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
              attributes: { content: 'Open', level: 3 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Apache-2.0 licensed top to bottom — no vendor lock-in, no surprise upgrades.',
              },
            },
          ],
        },
      ],
    },
  ],
};
