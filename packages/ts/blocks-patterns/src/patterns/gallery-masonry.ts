/**
 * "Masonry gallery" pattern.
 *
 * A heading-introduced `core/gallery` with six placeholder images. The
 * gallery is configured at three columns with `imageCrop: false` so the
 * thumbnails retain their natural aspect ratios — the closest the core
 * gallery comes to a true masonry layout without a dedicated block.
 */
import type { Pattern } from '../types.ts';

export const galleryMasonry: Pattern = {
  id: 'core/gallery-masonry',
  name: 'Masonry gallery',
  category: 'gallery',
  description:
    'A heading paired with a 3-column gallery that preserves image aspect ratios.',
  keywords: ['gallery', 'photos', 'images', 'masonry', 'portfolio'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'Recent work', level: 2 },
        },
        {
          type: 'core/gallery',
          attributes: {
            columns: 3,
            imageCrop: false,
            images: [
              { url: 'https://placehold.co/600x400', alt: 'Project 1' },
              { url: 'https://placehold.co/600x800', alt: 'Project 2' },
              { url: 'https://placehold.co/600x500', alt: 'Project 3' },
              { url: 'https://placehold.co/600x600', alt: 'Project 4' },
              { url: 'https://placehold.co/600x400', alt: 'Project 5' },
              { url: 'https://placehold.co/600x700', alt: 'Project 6' },
            ],
          },
        },
      ],
    },
  ],
};
