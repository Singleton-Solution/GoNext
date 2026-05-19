/**
 * "Image / text split — reversed" pattern.
 *
 * The mirror of `image-text-split`: copy on the left, image on the
 * right. Paired with its sibling, this is the "alternating row" shape
 * marketing pages cycle through to keep visual rhythm.
 */
import type { Pattern } from '../types.ts';

export const imageTextSplitReversed: Pattern = {
  id: 'core/image-text-split-reversed',
  name: 'Image + text split (reversed)',
  category: 'features',
  description:
    'A two-column feature row with copy on the left and an image on the right.',
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
              type: 'core/heading',
              attributes: { content: 'Plugins you can trust', level: 2 },
            },
            {
              type: 'core/paragraph',
              attributes: {
                content:
                  'Each plugin runs in an import-map-pinned sandbox with strict CSP and Trusted Types. You ship features without giving up the keys to the install.',
              },
            },
            {
              type: 'core/button',
              attributes: {
                text: 'See the plugin docs',
                url: '#plugins',
                style: 'outline',
              },
            },
          ],
        },
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
      ],
    },
  ],
};
