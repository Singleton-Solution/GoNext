/**
 * Tokens API helpers.
 *
 * Thin wrappers over `api-client` for the three /me/tokens endpoints.
 * Each helper is named after the verb it sends so callers don't have to
 * remember the URL — adding a new endpoint is a one-line export here.
 */
import { api } from '@/lib/api-client';
import type {
  IssueRequest,
  IssuedTokenView,
  TokenView,
} from './types';

interface ListResponse {
  data: TokenView[];
}

/**
 * GET /api/v1/me/tokens — list the current user's active tokens.
 *
 * Returns the empty array (not `undefined`) on success with no rows so
 * callers can render an empty state without a null check.
 */
export async function listTokens(): Promise<TokenView[]> {
  const res = await api.get<ListResponse>('/api/v1/me/tokens');
  return res?.data ?? [];
}

/**
 * POST /api/v1/me/tokens — issue a fresh token.
 *
 * The response carries the plaintext exactly once. Callers MUST hand
 * the result to TokenReveal (or another surface that gates dismissal)
 * — dropping the response on the floor means the operator loses the
 * token forever.
 */
export function issueToken(req: IssueRequest): Promise<IssuedTokenView> {
  return api.post<IssuedTokenView>('/api/v1/me/tokens', req);
}

/**
 * DELETE /api/v1/me/tokens/{id} — revoke a token.
 *
 * Resolves with void on 204; rejects with an ApiError on 404 or 5xx.
 */
export async function revokeToken(id: string): Promise<void> {
  await api.delete<void>(`/api/v1/me/tokens/${encodeURIComponent(id)}`);
}
