/**
 * Tests for the markdown renderer.
 *
 * We test the rendered HTML rather than the AST. The renderer pipeline is
 * an implementation detail; the contract is "you give me well-formed
 * markdown and I give you accessible HTML with stable heading ids."
 */
import { describe, expect, it } from 'vitest';
import { renderMarkdown, slugify } from '@/lib/mdx';

describe('slugify', () => {
  it('lowercases and dashes whitespace', () => {
    expect(slugify('Hello World')).toBe('hello-world');
  });
  it('strips punctuation', () => {
    expect(slugify('What is GoNext?')).toBe('what-is-gonext');
  });
  it('compacts repeated separators', () => {
    expect(slugify('a -- b __ c')).toBe('a-b-c');
  });
});

describe('renderMarkdown — headings', () => {
  it('emits id attributes for headings', async () => {
    const { html, headings } = await renderMarkdown('## Intro\n\nBody\n\n### Subsection\n');
    expect(html).toContain('<h2 id="intro">');
    expect(html).toContain('<h3 id="subsection">');
    expect(headings.length).toBe(2);
    expect(headings[0]).toMatchObject({ depth: 2, id: 'intro', text: 'Intro' });
  });

  it('attaches a visible anchor link per heading', async () => {
    const { html } = await renderMarkdown('## Hello\n');
    expect(html).toContain('class="heading-anchor"');
    expect(html).toContain('href="#hello"');
  });
});

describe('renderMarkdown — body constructs', () => {
  it('wraps plain text in paragraphs', async () => {
    const { html } = await renderMarkdown('Hello, world.\n');
    expect(html).toContain('<p>Hello, world.</p>');
  });

  it('parses inline bold and italic', async () => {
    const { html } = await renderMarkdown('A **bold** and *italic* word.\n');
    expect(html).toContain('<strong>bold</strong>');
    expect(html).toContain('<em>italic</em>');
  });

  it('parses links', async () => {
    const { html } = await renderMarkdown('See [the docs](https://example.com).\n');
    expect(html).toContain('<a href="https://example.com">the docs</a>');
  });

  it('parses unordered lists', async () => {
    const { html } = await renderMarkdown('- one\n- two\n- three\n');
    expect(html).toContain('<ul>');
    expect(html).toContain('<li>one</li>');
    expect(html).toContain('<li>three</li>');
  });

  it('renders fenced code blocks via shiki', async () => {
    const { html } = await renderMarkdown('```ts\nconst x = 1;\n```\n');
    expect(html).toContain('<pre');
    expect(html).toContain('shiki');
  });

  it('falls back to plain pre on unknown language', async () => {
    const { html } = await renderMarkdown('```\nliteral\n```\n');
    expect(html).toContain('literal');
  });

  it('parses GFM tables', async () => {
    const md = '| Col | Val |\n| --- | --- |\n| a | 1 |\n| b | 2 |\n';
    const { html } = await renderMarkdown(md);
    expect(html).toContain('<table>');
    expect(html).toContain('<th>Col</th>');
    expect(html).toContain('<td>a</td>');
  });

  it('parses blockquotes', async () => {
    const { html } = await renderMarkdown('> heads up\n');
    expect(html).toContain('<blockquote>');
  });
});
