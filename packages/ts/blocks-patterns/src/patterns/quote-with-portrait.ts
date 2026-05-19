/**
 * "Quote with portrait" pattern.
 *
 * A featured customer testimonial: a two-column row with the author's
 * portrait on the left and a pull-quote on the right. The quote block
 * already carries the citation, so the right column hosts the quote
 * directly without an extra heading.
 */
import type { Pattern } from '../types.ts';

export const quoteWithPortrait: Pattern = {
  id: 'core/quote-with-portrait',
  name: 'Quote with portrait',
  category: 'testimonials',
  description:
    'A featured testimonial with a portrait on the left and a pull-quote on the right.',
  keywords: ['testimonial', 'quote', 'portrait', 'review', 'social proof'],
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
              type: 'core/image',
              attributes: {
                url: 'https://placehold.co/320x320',
                alt: 'Jordan Reyes portrait',
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
                  'Migrating to GoNext gave our editors back two days a week. The block tree just clicks once you see it once.',
                citation: 'Jordan Reyes, Head of Content at Lumen Media',
                style: 'large',
              },
            },
          ],
        },
      ],
    },
  ],
};
