/**
 * "Stat counter row" pattern.
 *
 * Four-column row of "big number + label" stats — the social-proof
 * pattern every marketing page eventually grows. Each stat is a heading
 * (the number) plus a paragraph (the caption); the columns block
 * collapses to a stack on mobile by default.
 */
import type { Pattern } from '../types.ts';

export const statCounterRow: Pattern = {
  id: 'core/stat-counter-row',
  name: 'Stat counter row',
  category: 'features',
  description:
    'A four-column row of big-number statistics with short labels.',
  keywords: ['stats', 'metrics', 'numbers', 'kpis', 'social proof'],
  preview: './previews/placeholder.svg',
  blocks: [
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
              attributes: { content: '10k+', level: 2, align: 'center' },
            },
            {
              type: 'core/paragraph',
              attributes: { content: 'Sites in production', align: 'center' },
            },
          ],
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: '99.99%', level: 2, align: 'center' },
            },
            {
              type: 'core/paragraph',
              attributes: { content: 'Uptime last quarter', align: 'center' },
            },
          ],
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: '120ms', level: 2, align: 'center' },
            },
            {
              type: 'core/paragraph',
              attributes: { content: 'Median page TTFB', align: 'center' },
            },
          ],
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/heading',
              attributes: { content: '40+', level: 2, align: 'center' },
            },
            {
              type: 'core/paragraph',
              attributes: { content: 'First-party plugins', align: 'center' },
            },
          ],
        },
      ],
    },
  ],
};
