/**
 * "CTA banner" pattern.
 *
 * Compact full-width banner: heading, supporting line, single button.
 * Lives in a `core/group` with `tagName: aside` so it can sit between
 * content sections without competing with the main `<section>`s for
 * landmark semantics.
 */
import type { Pattern } from '../types.ts';

export const ctaBanner: Pattern = {
  id: 'core/cta-banner',
  name: 'CTA banner',
  category: 'cta',
  description:
    'A focused full-width banner with a headline, one-liner, and a single primary CTA.',
  keywords: ['cta', 'banner', 'conversion', 'newsletter'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'aside', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: {
            content: 'Ready to publish?',
            level: 2,
            align: 'center',
          },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'Spin up your first GoNext site in under five minutes — no credit card required.',
            align: 'center',
          },
        },
        {
          type: 'core/button',
          attributes: {
            text: 'Create your site',
            url: '#signup',
            style: 'fill',
            align: 'center',
          },
        },
      ],
    },
  ],
};
