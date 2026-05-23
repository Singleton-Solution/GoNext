/**
 * "Image / text split" pattern.
 *
 * Two-column "feature spotlight": image on the left, heading + body +
 * CTA on the right. The reversed sibling (`image-text-split-reversed`)
 * is the canonical "alternating row" treatment marketing pages lean on.
 */
import type { Pattern } from '../types.ts';

export const imageTextSplit: Pattern = {
  id: 'core/image-text-split',
  name: 'Image + text split',
  category: 'features',
  description:
    'A two-column feature row with an image on the left and copy on the right.',
  keywords: ['feature', 'split', 'image', 'media', 'alternating'],
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
                url: 'https://placehold.co/640x480',
                alt: 'Feature spotlight image',
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
              attributes: { content: 'Compose pages from blocks', level: 2 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Every page on a GoNext site is a tree of typed blocks. Authors compose, the editor validates, and the renderer ships HTML the browser is happy with.',
              },
            },
            {
              type: 'core/button',
              attributes: {
                text: 'Learn more',
                url: '#feature',
                style: 'outline',
              },
            },
          ],
        },
      ],
    },
  ],
};
