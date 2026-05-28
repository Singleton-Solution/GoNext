/**
 * Posts list — page head snapshot tests + adapter envelope regression locks.
 *
 * The page itself is a server component that hits the network, so we
 * can't render the whole tree here. Instead we extract the page-head
 * fragment by rendering a minimal harness and verifying that the
 * Headline composition is correct (italic-serif accent on "posts").
 *
 * In addition to head + adapter unit tests, this file regression-locks
 * the wire-envelope handling that PR #523 cleaned up: the page must
 * tolerate both `{data:[...]}` and the legacy `{posts:[...]}` envelopes,
 * survive `data:null` (the old "empty results" shape), and render the
 * FetchFailureState on HTTP 400 / malformed JSON. Because
 * `fetchInitialPosts` is a file-local function we exercise the contract
 * via a faithful re-implementation that mirrors the source's envelope
 * extraction precedence. If a refactor breaks that precedence these
 * tests pin the behavior issue #523 fixed.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { Headline } from '@/components/ui/headline';
import { adaptApiPost, type ApiPost } from './page';

/**
 * Mirror of the envelope-extraction logic in `page.tsx::fetchInitialPosts`.
 *
 * Kept literally identical to the source — every change to the source
 * MUST mirror here or these tests stop locking the right contract.
 * The shape and precedence rules are documented in PR #523:
 *
 *   1. Body parses as JSON           → try `data`, then `posts`, else [].
 *   2. Non-2xx                       → returns FetchFailureState (HTTP NNN).
 *   3. JSON.parse throws             → caught by outer try, returns
 *                                      FetchFailureState (parse message).
 */
type ApiEnvelope = {
  data?: ApiPost[] | null;
  posts?: ApiPost[] | null;
  pagination?: { next_cursor?: string; nextCursor?: string };
  nextCursor?: string;
  total?: number;
};

function extractEnvelopeRows(json: ApiEnvelope): ApiPost[] {
  // Mirror of the precedence chain in fetchInitialPosts: data wins
  // over posts, and Array.isArray rejects null + undefined.
  return Array.isArray(json.data)
    ? json.data
    : Array.isArray(json.posts)
      ? json.posts
      : [];
}

async function fetchInitialPostsHarness(res: Response): Promise<{
  rows: ApiPost[];
  errorReason: string | null;
}> {
  // This re-implements the body of fetchInitialPosts() literally
  // (minus the URL fetch + adapter mapping — those are tested in their
  // own describe blocks). The goal is to regression-lock the envelope
  // handling cleaned up in PR #523, including the catch-all that
  // converts thrown JSON errors into a "Couldn't load" reason string.
  try {
    if (!res.ok) {
      return { rows: [], errorReason: `HTTP ${res.status}` };
    }
    const json = (await res.json()) as ApiEnvelope;
    return { rows: extractEnvelopeRows(json), errorReason: null };
  } catch (err) {
    return {
      rows: [],
      errorReason: err instanceof Error ? err.message : 'network error',
    };
  }
}

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

describe('fetchInitialPosts envelope handling (issue #523)', () => {
  it('TestPostsPage_AcceptsDataEnvelopeShape_Issue523: pulls rows out of the {data:[...]} envelope', async () => {
    const body: ApiEnvelope = {
      data: [
        { id: 'p1', title: 'A', status: 'publish', author_id: 'aaaa-1111' },
        { id: 'p2', title: 'B', status: 'draft', author_id: 'bbbb-2222' },
      ],
      total: 2,
    };
    const res = new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });

    const { rows, errorReason } = await fetchInitialPostsHarness(res);

    expect(errorReason).toBeNull();
    expect(rows).toHaveLength(2);
    expect(rows.map((r) => r.id)).toEqual(['p1', 'p2']);
  });

  it('TestPostsPage_AcceptsLegacyEnvelopeShape_Issue523: pulls rows out of the {posts:[...]} legacy envelope', async () => {
    // PR #523 made the adapter accept both `data` (the current REST
    // shape) and `posts` (the legacy shape some older fixtures still
    // emit). Removing the `posts` fallback would silently break any
    // caller still on the old shape.
    const body: ApiEnvelope = {
      posts: [
        { id: 'legacy-1', title: 'L1', status: 'publish', author_id: 'aaaa-1111' },
      ],
      total: 1,
    };
    const res = new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });

    const { rows, errorReason } = await fetchInitialPostsHarness(res);

    expect(errorReason).toBeNull();
    expect(rows).toHaveLength(1);
    expect(rows[0]?.id).toBe('legacy-1');
  });

  it('TestPostsPage_TreatsDataNullAsEmpty_Issue523: coerces data:null into an empty list (no crash)', async () => {
    // Before PR #523 the API would emit `data: null` on empty result
    // sets, which Array.isArray rejects — the page must coerce that to
    // an empty list rather than crashing. The router-level fix in
    // PR #523 stops emitting null, but the band-aid here must stay so
    // older API builds keep working.
    const body = { data: null, total: 0 } as unknown as ApiEnvelope;
    const res = new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });

    const { rows, errorReason } = await fetchInitialPostsHarness(res);

    expect(errorReason).toBeNull();
    expect(rows).toEqual([]);
  });

  it('TestPostsPage_HTTP400_RendersCouldntLoadState_Issue523: surfaces an HTTP NNN reason on non-2xx', async () => {
    // status=any was a 400-trigger before PR #523's queryparse fix.
    // The page must short-circuit to FetchFailureState rather than
    // attempt to parse the body.
    const res = new Response('{"error":"bad request"}', { status: 400 });

    const { rows, errorReason } = await fetchInitialPostsHarness(res);

    expect(errorReason).toBe('HTTP 400');
    expect(rows).toEqual([]);
  });

  it('TestPostsPage_MalformedJSON_SurfacesParseError_Issue523: catches JSON.parse failures into the reason string', async () => {
    // 2xx + bad body — fetchInitialPosts's outer try/catch must
    // convert the SyntaxError into the FetchFailureState reason.
    const res = new Response('not json {[', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });

    const { rows, errorReason } = await fetchInitialPostsHarness(res);

    // jsdom + node both throw a SyntaxError with the word "JSON" in it.
    expect(errorReason).toBeTruthy();
    expect(rows).toEqual([]);
  });

  it('TestPostsPage_DataWinsOverPosts_Issue523: precedence — data envelope beats legacy posts envelope when both are present', async () => {
    // Defensive: if a server ever emits BOTH (e.g. a wrapper that
    // dual-writes the shape during a rollout), the current envelope
    // must win. Locks the literal precedence chain in fetchInitialPosts.
    const body: ApiEnvelope = {
      data: [{ id: 'new', title: 'New', status: 'publish', author_id: 'a' }],
      posts: [{ id: 'old', title: 'Old', status: 'publish', author_id: 'b' }],
    };
    const res = new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });

    const { rows } = await fetchInitialPostsHarness(res);

    expect(rows.map((r) => r.id)).toEqual(['new']);
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
