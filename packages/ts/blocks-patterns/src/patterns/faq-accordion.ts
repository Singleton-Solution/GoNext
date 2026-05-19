/**
 * "FAQ accordion" pattern.
 *
 * Frequently-asked-questions section: a heading and a series of
 * question/answer pairs. The editor doesn't yet ship a true `<details>`
 * accordion block, so the pattern uses alternating heading + paragraph
 * pairs wrapped in a semantic `<section>`. A later issue will swap each
 * pair for a real accordion item without breaking the outer shape.
 */
import type { Pattern } from '../types.ts';

export const faqAccordion: Pattern = {
  id: 'core/faq-accordion',
  name: 'FAQ accordion',
  category: 'features',
  description:
    'A heading paired with a stack of question/answer entries — the shape every product page lands on.',
  keywords: ['faq', 'questions', 'help', 'support', 'accordion'],
  preview: './previews/placeholder.svg',
  blocks: [
    {
      type: 'core/group',
      attributes: { tagName: 'section', layout: 'default' },
      innerBlocks: [
        {
          type: 'core/heading',
          attributes: { content: 'Frequently asked questions', level: 2 },
        },
        {
          type: 'core/heading',
          attributes: { content: 'How do I get started?', level: 3 },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'Sign up for a free account, follow the quickstart, and you will have your first site live within five minutes.',
          },
        },
        {
          type: 'core/heading',
          attributes: { content: 'Can I bring my own theme?', level: 3 },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'Yes — themes are plain TS packages. Drop one into your apps directory and the editor picks it up on the next reload.',
          },
        },
        {
          type: 'core/heading',
          attributes: { content: 'Is GoNext production-ready?', level: 3 },
        },
        {
          type: 'core/paragraph',
          attributes: {
            content:
              'The core stack is Apache-2.0 and battle-tested in real deployments. See the changelog for the per-release status.',
          },
        },
      ],
    },
  ],
};
