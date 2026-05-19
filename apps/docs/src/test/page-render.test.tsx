/**
 * Smoke test for the page render path.
 *
 * Builds a fixture, asks the renderer for HTML, and feeds it into a JSDOM
 * document the way the route would. We assert:
 *  - Frontmatter parsing (title comes from the YAML block).
 *  - Heading anchors are present and unique.
 *  - Code blocks survive the renderer.
 */
import { describe, expect, it, afterEach } from 'vitest';
import { findPage } from '@/lib/content';
import { renderMarkdown } from '@/lib/mdx';
import { cleanupFixture, makeFixture } from './fixtures';

describe('page render pipeline', () => {
  let root: string;
  afterEach(async () => {
    if (root) await cleanupFixture(root);
  });

  it('renders a known md file with frontmatter, headings, and a code block', async () => {
    root = await makeFixture([
      {
        path: 'docs/01-example.md',
        body: [
          '---',
          'title: Example Doc',
          'description: A short example.',
          '---',
          '',
          '## First section',
          '',
          'Some prose with **emphasis** and a [link](https://example.com).',
          '',
          '```ts',
          'export const x = 1;',
          '```',
          '',
          '## Second section',
          '',
          '- a',
          '- b',
          '',
        ].join('\n'),
      },
    ]);
    const page = await findPage('docs', ['01-example'], root);
    expect(page).not.toBeNull();
    expect(page?.meta.title).toBe('Example Doc');
    expect(page?.meta.description).toBe('A short example.');

    const rendered = await renderMarkdown(page!.body);
    // Headings indexed for the right-rail TOC.
    expect(rendered.headings.map((h) => h.id)).toEqual(['first-section', 'second-section']);
    // Stable, unique anchor ids land in the HTML.
    expect(rendered.html).toContain('id="first-section"');
    expect(rendered.html).toContain('id="second-section"');
    // Inline formatting works.
    expect(rendered.html).toContain('<strong>emphasis</strong>');
    expect(rendered.html).toContain('<a href="https://example.com">link</a>');
    // Code block landed.
    expect(rendered.html).toContain('<pre');
    expect(rendered.html).toContain('shiki');
  });
});
