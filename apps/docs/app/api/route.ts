/**
 * /api — OpenAPI proxy for the API Reference view.
 *
 * In production this fetches the OpenAPI spec served by `apps/api`
 * (typically at `/openapi.json`) and forwards it through with a small
 * amount of CORS-safe rewriting. For the static build we serve a
 * placeholder so the route is reachable.
 *
 * The spec URL is controlled by the `GONEXT_OPENAPI_URL` env var; if
 * unset we fall back to a stub so the build does not break in environments
 * without a running API.
 */
import { NextResponse } from 'next/server';

const STUB_SPEC = {
  openapi: '3.1.0',
  info: {
    title: 'GoNext API (stub)',
    version: '0.0.0',
    description:
      'This is a placeholder. Set GONEXT_OPENAPI_URL at build/runtime to proxy the real spec from apps/api.',
  },
  paths: {},
};

export async function GET(): Promise<NextResponse> {
  const target = process.env.GONEXT_OPENAPI_URL;
  if (!target) {
    return NextResponse.json(STUB_SPEC, {
      headers: { 'cache-control': 'no-store' },
    });
  }
  try {
    const upstream = await fetch(target, {
      // The OpenAPI doc is small and changes only at release boundaries —
      // 5-minute edge cache is fine and shields the API from incidental
      // traffic spikes from doc-page rendering.
      next: { revalidate: 300 },
    });
    if (!upstream.ok) {
      return NextResponse.json(
        { error: 'upstream_unreachable', status: upstream.status },
        { status: 502 },
      );
    }
    const body = await upstream.json();
    return NextResponse.json(body, {
      headers: { 'cache-control': 'public, max-age=300' },
    });
  } catch (err) {
    return NextResponse.json(
      { error: 'fetch_failed', message: (err as Error).message },
      { status: 502 },
    );
  }
}
