/**
 * Webhook subscriptions — server actions / API wrappers.
 *
 * Same shape as the jobs/dlq actions: each function wraps a single
 * endpoint and returns the parsed body. Keeping these out of
 * 'use server' files makes the call site obvious and lets tests stub
 * the underlying api client without Next.js test-harness magic.
 */
import { api } from '@/app/api-client';
import type {
  Delivery,
  DeliveryListResponse,
  EventCatalogResponse,
  Subscription,
  SubscriptionCreate,
  SubscriptionListResponse,
  SubscriptionUpdate,
  SubscriptionWithSecret,
  TestResult,
} from './types';

const BASE = '/api/v1/admin/webhooks';

export interface ListParams {
  limit?: number;
  cursor?: string;
}

export function listSubscriptions(params: ListParams = {}): Promise<SubscriptionListResponse> {
  const search = new URLSearchParams();
  if (params.limit !== undefined) search.set('limit', String(params.limit));
  if (params.cursor) search.set('cursor', params.cursor);
  const qs = search.toString();
  return api.get<SubscriptionListResponse>(qs ? `${BASE}?${qs}` : BASE);
}

export function getSubscription(id: string): Promise<Subscription> {
  return api.get<Subscription>(`${BASE}/${encodeURIComponent(id)}`);
}

/**
 * Create a subscription. The response includes the raw HMAC secret as
 * a hex string — surface it to the operator immediately because no
 * subsequent endpoint returns it.
 */
export function createSubscription(
  body: SubscriptionCreate,
): Promise<SubscriptionWithSecret> {
  return api.post<SubscriptionWithSecret>(BASE, body);
}

export function updateSubscription(
  id: string,
  body: SubscriptionUpdate,
): Promise<Subscription> {
  return api.patch<Subscription>(`${BASE}/${encodeURIComponent(id)}`, body);
}

export function deleteSubscription(id: string): Promise<unknown> {
  return api.delete(`${BASE}/${encodeURIComponent(id)}`);
}

export function disableSubscription(id: string): Promise<Subscription> {
  return api.post<Subscription>(`${BASE}/${encodeURIComponent(id)}/disable`);
}

export function enableSubscription(id: string): Promise<Subscription> {
  return api.post<Subscription>(`${BASE}/${encodeURIComponent(id)}/enable`);
}

/**
 * Trigger a synchronous test send. The result contains the HTTP
 * status the subscriber returned, the round-trip duration, and a
 * `delivered` boolean that's true iff the status was 2xx.
 */
export function testSubscription(id: string): Promise<TestResult> {
  return api.post<TestResult>(`${BASE}/${encodeURIComponent(id)}/test`);
}

export function listDeliveries(
  id: string,
  params: ListParams = {},
): Promise<DeliveryListResponse> {
  const search = new URLSearchParams();
  if (params.limit !== undefined) search.set('limit', String(params.limit));
  if (params.cursor) search.set('cursor', params.cursor);
  const qs = search.toString();
  const base = `${BASE}/${encodeURIComponent(id)}/deliveries`;
  return api.get<DeliveryListResponse>(qs ? `${base}?${qs}` : base);
}

export function listEventCatalog(): Promise<EventCatalogResponse> {
  return api.get<EventCatalogResponse>(`${BASE}/events`);
}

// Re-export for tests that need to type-check a Delivery payload.
export type { Delivery };
