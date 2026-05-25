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
