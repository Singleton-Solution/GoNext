/**
 * Redirects — API wrappers.
 *
 * One function per endpoint; each returns the parsed JSON body or
 * throws ApiError. Same shape as the webhooks/actions.ts file so
 * call sites stay uniform across the admin surface.
 */
import { api } from '@/lib/api-client';
import type {
  Redirect,
  RedirectInput,
  RedirectListResponse,
  RegexTestRequest,
  RegexTestResponse,
} from './types';

const BASE = '/api/v1/admin/redirects';

export interface ListParams {
  limit?: number;
  before?: string;
  search?: string;
}

export function listRedirects(params: ListParams = {}): Promise<RedirectListResponse> {
  const search = new URLSearchParams();
  if (params.limit !== undefined) search.set('limit', String(params.limit));
  if (params.before) search.set('before', params.before);
  if (params.search) search.set('search', params.search);
  const qs = search.toString();
  return api.get<RedirectListResponse>(qs ? `${BASE}?${qs}` : BASE);
}

export function listTopRedirects(limit = 20): Promise<RedirectListResponse> {
  return api.get<RedirectListResponse>(`${BASE}/top?limit=${encodeURIComponent(String(limit))}`);
}

export function getRedirect(id: string): Promise<Redirect> {
  return api.get<Redirect>(`${BASE}/${encodeURIComponent(id)}`);
}

export function createRedirect(input: RedirectInput): Promise<Redirect> {
  return api.post<Redirect>(BASE, input);
}

export function updateRedirect(id: string, input: RedirectInput): Promise<Redirect> {
  return api.put<Redirect>(`${BASE}/${encodeURIComponent(id)}`, input);
}

export function deleteRedirect(id: string): Promise<void> {
  return api.delete<void>(`${BASE}/${encodeURIComponent(id)}`);
}

export function testRegex(input: RegexTestRequest): Promise<RegexTestResponse> {
  return api.post<RegexTestResponse>(`${BASE}/test-regex`, input);
}
