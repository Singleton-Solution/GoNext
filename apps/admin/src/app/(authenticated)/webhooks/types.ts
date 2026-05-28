/**
 * Webhook subscriptions — shared types.
 *
 * Shapes mirror the JSON envelope returned by the Go admin webhooks
 * package (apps/api/internal/admin/webhooks). When the Go contract
 * changes, this file changes with it — there's no separate mapping
 * step, the fetch layer just asserts the response as one of these
 * types.
 *
 * Issue #514 follow-up: the base wire fields (`id`, `url`, `events`,
 * `active`, `created_at`) are sourced from the OpenAPI spec via
 * `@gonext/api-types`. The admin endpoint emits a richer projection
 * (delivery telemetry, name, updated_at) that the spec doesn't model
 * yet — those fields stay as a local intersection so a spec change
 * still flags drift on the shared fields.
 */
import type { components } from '@gonext/api-types';

type WebhookSpec = components['schemas']['Webhook'];

export type Subscription = Pick<
  WebhookSpec,
  'id' | 'url' | 'events' | 'active' | 'created_at'
> & {
  name: string;
  created_by?: string;
  updated_at: string;
  last_delivery_at?: string;
  last_delivery_status?: 'success' | 'retry' | 'failed' | '';
  last_delivery_response_code?: number;
  consecutive_failures: number;
  degraded_at?: string;
};

/**
 * Returned by POST /webhooks — includes the raw HMAC secret as a hex
 * string. This is the ONLY response that surfaces the secret. The UI
 * must show it to the operator and instruct them to copy it; later
 * GETs return only the Subscription shape.
 */
export interface SubscriptionWithSecret extends Subscription {
  secret: string;
}

/**
 * Body for POST /webhooks. Extends the spec's `WebhookCreate` with the
 * admin-only `name` field (the spec doesn't model it yet — tracked as
 * follow-up). `active` is required by the spec; we relax it here so
 * the create form can omit it and let the server default kick in.
 */
export type SubscriptionCreate = Omit<
  components['schemas']['WebhookCreate'],
  'secret' | 'active'
> & {
  name: string;
  active?: boolean;
};

/**
 * Body for PATCH /webhooks/{id}. The admin form additionally allows
 * editing `name`, which the spec doesn't model yet.
 */
export type SubscriptionUpdate = components['schemas']['WebhookUpdate'] & {
  name?: string;
};

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
