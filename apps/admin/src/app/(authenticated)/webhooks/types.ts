/**
 * Webhook subscriptions — shared types.
 *
 * Shapes mirror the JSON envelope returned by the Go admin webhooks
 * package (apps/api/internal/admin/webhooks). When the Go contract
 * changes, this file changes with it — there's no separate mapping
 * step, the fetch layer just asserts the response as one of these
 * types.
 */

export interface Subscription {
  id: string;
  name: string;
  url: string;
  events: string[];
  active: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
  last_delivery_at?: string;
  last_delivery_status?: 'success' | 'retry' | 'failed' | '';
  last_delivery_response_code?: number;
  consecutive_failures: number;
  degraded_at?: string;
}

/**
 * Returned by POST /webhooks — includes the raw HMAC secret as a hex
 * string. This is the ONLY response that surfaces the secret. The UI
 * must show it to the operator and instruct them to copy it; later
 * GETs return only the Subscription shape.
 */
export interface SubscriptionWithSecret extends Subscription {
  secret: string;
}

export interface SubscriptionCreate {
  name: string;
  url: string;
  events: string[];
  active?: boolean;
}

export interface SubscriptionUpdate {
  name?: string;
  url?: string;
  events?: string[];
  active?: boolean;
}

export interface Delivery {
  id: number;
  subscription_id: string;
  event_id: string;
  event_type: string;
  attempt: number;
  status: 'success' | 'retry' | 'failed' | 'test';
  response_code: number;
  duration_ms: number;
  response_body_preview?: string;
  error?: string;
  delivered_at: string;
}

export interface TestResult {
  delivered: boolean;
  response_code: number;
  duration_ms: number;
  error?: string;
}

export interface EventDescriptor {
  name: string;
  description: string;
}

export interface PaginatedResponse<T> {
  data: T[];
  pagination: {
    next_cursor: string;
    prev_cursor?: string;
  };
}

export type SubscriptionListResponse = PaginatedResponse<Subscription>;
export type DeliveryListResponse = PaginatedResponse<Delivery>;
export interface EventCatalogResponse {
  data: EventDescriptor[];
}
