/**
 * "Contact" pattern.
 *
 * The block editor scaffold doesn't yet ship a form block — so this
 * pattern delivers the *contact section* every site needs: a heading,
 * a "how to reach us" paragraph, and an explicit button linking to an
 * email or contact endpoint. A later issue will swap in a real form
 * block; the pattern shape stays the same.
 */
import type { Pattern } from '../types.ts';

export const contactForm: Pattern = {
  id: 'core/contact-form',
  name: 'Contact section',
  category: 'contact',
  description:
    'A simple contact section with a headline, instructions, and an email CTA.',
  keywords: ['contact', 'email', 'form', 'support'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'Get in touch', level: 2 },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'Have a question, a partnership idea, or feedback? We read every message and reply within one business day.',
          },
        },
        {
          type: 'core/list',
          attributes: {
            ordered: false,
            values: [
              'Email: hello@example.com',
              'Mailing list: monthly product updates',
              'Office hours: Wednesdays at 15:00 UTC',
            ],
          },
        },
        {
          type: 'core/button',
          attributes: {
            text: 'Email us',
            url: 'mailto:hello@example.com',
            style: 'fill',
          },
        },
      ],
    },
  ],
};
