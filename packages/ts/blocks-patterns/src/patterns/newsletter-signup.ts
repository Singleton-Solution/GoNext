/**
 * "Newsletter signup" pattern.
 *
 * Compact "subscribe to the newsletter" section — heading, one-line
 * value proposition, and a primary subscribe CTA. The dedicated form
 * block does not ship yet, so the button links to the subscribe
 * endpoint; once the form block lands, this pattern updates in place.
 */
import type { Pattern } from '../types.ts';

export const newsletterSignup: Pattern = {
  id: 'core/newsletter-signup',
  name: 'Newsletter signup',
  category: 'cta',
  description:
    'A compact "subscribe to the newsletter" section with a single primary CTA.',
  keywords: ['newsletter', 'subscribe', 'email', 'signup', 'updates'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'aside', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: {
            content: 'Get the monthly digest',
            level: 2,
            align: 'center',
          },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'One email a month with the highlights — new releases, deep dives, and the patterns we are most excited about.',
            align: 'center',
          },
        },
        {
          type: 'core/button',
          attributes: {
            text: 'Subscribe',
            url: '#subscribe',
            style: 'fill',
            align: 'center',
          },
        },
      ],
    },
  ],
};
