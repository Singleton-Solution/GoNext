/**
 * "Testimonial grid" pattern.
 *
 * Two columns, each hosting a `core/quote` with author attribution.
 * Quotes use `style: large` so theme CSS can give them the heavier
 * pull-quote treatment without per-quote overrides.
 */
import type { Pattern } from '../types.ts';

export const testimonialGrid: Pattern = {
  id: 'core/testimonial-grid',
  name: 'Testimonial grid',
  category: 'testimonials',
  description:
    'A two-column grid of pull-quoted testimonials with author attribution.',
  keywords: ['testimonials', 'reviews', 'quotes', 'social proof'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/columns',
      attributes: { columns: 2, isStackedOnMobile: true },
      innerBlocks: [
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/quote',
              attributes: {
                value:
                  'GoNext gave us a publishing pipeline our editors actually enjoy using.',
                citation: 'Jordan Reyes, Head of Content at Lumen Media',
                style: 'large',
              },
            },
          ],
        },
        {
          type: 'core/group',
          attributes: { tagName: 'div', layout: 'default' },
          innerBlocks: [
            {
              type: 'core/quote',
              attributes: {
                value:
                  'The plugin model just clicks — we shipped a paywall in a weekend, not a quarter.',
                citation: 'Sam Patel, CTO at Driftwood',
                style: 'large',
              },
            },
          ],
        },
      ],
    },
  ],
};
