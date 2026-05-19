/**
 * "Hero with CTA" pattern.
 *
 * The canonical landing-page opener: a wide heading, a supporting
 * paragraph, and a pair of buttons. The hero is wrapped in a `core/group`
 * with `tagName: section` so theme CSS can target the hero region as a
 * semantic landmark.
 *
 * The BlockTree is intentionally lean — patterns are starter shapes, not
 * production copy. Authors edit the strings after insertion; the
 * inspector schemas keep them within bounds.
 */
import type { Pattern } from '../types.ts';

export const heroWithCta: Pattern = {
  id: 'core/hero-with-cta',
  name: 'Hero with CTA',
  category: 'hero',
  description:
    'A full-width hero with a headline, supporting copy, and primary/secondary call-to-action buttons.',
  keywords: ['landing', 'banner', 'header', 'splash'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: {
        tagName: 'section',
        layout: 'default',
      },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: {
            content: 'Build with GoNext',
            level: 1,
            align: 'center',
          },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'A modern, plugin-friendly publishing platform that puts authors first. Start a site in seconds.',
            align: 'center',
          },
        },
        {
          type: 'core/group',
          attributes: {
            tagName: 'div',
            layout: 'flex',
          },
          innerBlocks: [
            {
              type: 'core/button',
              attributes: {
                text: 'Get started',
                url: '#get-started',
                style: 'fill',
              },
            },
            {
              type: 'core/button',
              attributes: {
                text: 'Read the docs',
                url: '#docs',
                style: 'outline',
              },
            },
          ],
        },
      ],
    },
  ],
};
