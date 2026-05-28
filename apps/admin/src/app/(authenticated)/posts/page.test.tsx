/**
 * Posts list — page head snapshot tests.
 *
 * The page itself is a server component that hits the network, so we
 * can't render the whole tree here. Instead we extract the page-head
 * fragment by rendering a minimal harness and verifying that the
 * Headline composition is correct (italic-serif accent on "posts").
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { Headline } from '@/components/ui/headline';
import { adaptApiPost, type ApiPost } from './page';

describe('adaptApiPost — author display fallback (issue #515)', () => {
  it('falls back to last 8 chars of the UUID when the API omits a display name', () => {
    // 36-char UUID — the realistic shape coming back from the list
    // endpoint today (no `users` join, so no display_name).
    const apiPost: ApiPost = {
      id: 'post-1',
      title: 'Hello',
      status: 'publish',
      author_id: '550e8400-e29b-41d4-a716-446655440000',
    };
    const adapted = adaptApiPost(apiPost);

    expect(adapted.author.id).toBe('550e8400-e29b-41d4-a716-446655440000');
    // Last 8 chars of the UUID — short enough for the table cell,
    // long enough to disambiguate.
    expect(adapted.author.displayName).toBe('55440000');
  });

  it('uses the API-supplied display name when present (no fallback)', () => {
    const apiPost: ApiPost = {
      id: 'post-2',
      title: 'Hi',
      status: 'publish',
      author_id: '550e8400-e29b-41d4-a716-446655440000',
      author: { display_name: 'Ada Lovelace' },
    };
    expect(adaptApiPost(apiPost).author.displayName).toBe('Ada Lovelace');
  });

  it('leaves commentsCount at 0 — list endpoint does not compute it', () => {
    const apiPost: ApiPost = {
      id: 'post-3',
      title: 'C',
      status: 'draft',
      author_id: 'abcdefgh',
    };
    expect(adaptApiPost(apiPost).commentsCount).toBe(0);
  });
});

describe('Posts page head', () => {
  it('renders the brand "All posts." headline with the italic accent', () => {
    const { container } = render(
      <Headline as="h1" size="page" className="text-[44px]">
        All <em>posts</em>.
      </Headline>,
    );
    const h1 = container.querySelector('h1');
    expect(h1).not.toBeNull();
    expect(h1?.textContent).toMatch(/All\s+posts\./);
    expect(h1?.querySelector('em')?.textContent).toBe('posts');
  });

  it('matches the page-head snapshot', () => {
    const { container } = render(
      <Headline as="h1" size="page" className="text-[44px]">
        All <em>posts</em>.
      </Headline>,
    );
    expect(container.firstChild).toMatchSnapshot();
  });
});
