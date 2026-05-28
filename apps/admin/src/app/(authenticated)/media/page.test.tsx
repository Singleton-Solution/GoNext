/**
 * Media page — server-component coercion regression tests (issue #523).
 *
 * The admin media list endpoint historically returned `data: null` for
 * empty libraries (Postgres NULL → Go nil-slice → JSON null). The page
 * coerces null → [] before handing the result to the <MediaGrid>
 * client island, because the client assumes `data` is always an array
 * and calling `.length` on null throws.
 *
 * PR #523 added a router-level fix on the API side (router.Page[T]
 * coerces nil Data to []), but the page-side band-aid stays in place
 * so older API builds keep working. These tests lock the coercion
 * logic.
 *
 * Note: media/page.tsx is a Server Component and exports only the
 * default async function. We cannot import `fetchInitial` directly,
 * so we test by mocking `@/lib/server-api`, awaiting the page's
 * default export, and verifying the resulting JSX prop tree carries a
 * safe array shape into the MediaGrid client.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// MediaGrid is a complex client component with hooks; we don't need
// to render it for these tests. Replace it with a transparent shim
// so we can read the props the server component passed in.
const mediaGridProps: { value: unknown } = { value: undefined };
vi.mock('./components/MediaGrid', () => ({
  MediaGrid: (props: unknown) => {
    mediaGridProps.value = props;
    // Render nothing — props introspection is what we care about.
    return null;
  },
}));

const serverApiFetchMock = vi.fn();
vi.mock('@/lib/server-api', () => ({
  serverApiFetch: (...args: unknown[]) => serverApiFetchMock(...args),
}));

import MediaPage from './page';

beforeEach(() => {
  serverApiFetchMock.mockReset();
  mediaGridProps.value = undefined;
});

afterEach(() => {
  vi.useRealTimers();
});

/** Build a Response shim for serverApiFetch to return. */
function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('Media page server coercion (issue #523)', () => {
  it('TestMediaPage_CoercesDataNullToEmptyArray_Issue523: API data:null is normalised to []', async () => {
    // Reproduce the failure mode: the API returned `data: null` and
    // the client crashed reading `.length`. The page-level coerce in
    // page.tsx is what saves it.
    serverApiFetchMock.mockResolvedValueOnce(
      jsonRes({ data: null, pagination: { next_cursor: '' } }),
    );

    // Awaiting an async server component is the only way to drive
    // its rendering in vitest without a full React server runtime.
    const tree = await MediaPage();
    // Render the resulting JSX through React's virtual tree just to
    // trigger the MediaGrid shim's prop-capture.
    const { render } = await import('@testing-library/react');
    render(tree);

    const props = mediaGridProps.value as {
      initialData: { data: unknown[] };
    };
    expect(props.initialData.data).toEqual([]);
    expect(Array.isArray(props.initialData.data)).toBe(true);
  });

  it('TestMediaPage_PassesThroughArrayData_Issue523: real arrays are not mutated', async () => {
    // Sanity: when the API returns the canonical shape the page
    // does not corrupt the data on its way to the client.
    const data = [
      {
        id: 'a',
        filename: 'a.png',
        mime_type: 'image/png',
        byte_size: 100,
        alt_text: '',
        caption: '',
        storage_key: 'k/a',
        uploader_id: 'u',
        created_at: '2026-05-17T00:00:00Z',
        updated_at: '2026-05-17T00:00:00Z',
        tags: [],
      },
    ];
    serverApiFetchMock.mockResolvedValueOnce(
      jsonRes({ data, pagination: { next_cursor: '' } }),
    );

    const tree = await MediaPage();
    const { render } = await import('@testing-library/react');
    render(tree);

    const props = mediaGridProps.value as {
      initialData: { data: unknown[] };
    };
    expect(props.initialData.data).toHaveLength(1);
  });

  it('TestMediaPage_FallbackOnFetchFailure_Issue523: non-2xx surfaces an empty list (no crash)', async () => {
    // Server-side fetch failure path. The page returns null from
    // fetchInitial and falls through to a safe `{data:[]}` default.
    serverApiFetchMock.mockResolvedValueOnce(
      new Response('forbidden', { status: 403 }),
    );

    const tree = await MediaPage();
    const { render } = await import('@testing-library/react');
    render(tree);

    const props = mediaGridProps.value as {
      initialData: { data: unknown[] };
    };
    expect(props.initialData.data).toEqual([]);
  });

  it('TestMediaPage_FallbackOnFetchThrow_Issue523: thrown network error surfaces an empty list', async () => {
    // serverApiFetch can also throw (DNS, abort, etc.). The page's
    // try/catch is the last line of defence.
    serverApiFetchMock.mockRejectedValueOnce(new Error('ECONNRESET'));

    const tree = await MediaPage();
    const { render } = await import('@testing-library/react');
    render(tree);

    const props = mediaGridProps.value as {
      initialData: { data: unknown[] };
    };
    expect(props.initialData.data).toEqual([]);
  });
});
