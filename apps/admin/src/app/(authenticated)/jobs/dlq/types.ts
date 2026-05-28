/**
 * DLQ admin — shared types.
 *
 * The shapes here mirror the JSON envelope returned by
 * `GET /api/v1/admin/jobs/dlq` (and `.../{id}`). Keeping the shape on
 * the client matches one-for-one keeps the network layer dumb: no
 * mapping step that has to be kept in sync with the Go type, just
 * `as ArchivedTask` after the parse.
 *
 * Field nullability follows the Go side: optional fields use `?` and
 * map to `omitempty` JSON tags. When the Go contract changes, this
 * file changes with it.
 *
 * Issue #514 follow-up: `ArchivedTask` is sourced from the OpenAPI
 * spec via `@gonext/api-types` so the wire fields are in lock-step
 * with the server's struct tags. The list rendering depends on
 * `failed_at` being present (the row's primary timestamp), so we
 * tighten that field locally — once the spec marks it required this
 * override can go away.
 */
import type { components } from '@gonext/api-types';

/**
 * A single archived (dead-letter) task as returned by the list and
 * detail endpoints.
 *
 * `payload` is included only by the detail endpoint; the list omits
 * it to keep the table envelope tight (the preview substitutes).
 *
 * `redacted_fields` is present only when the task has an active
 * redaction record — the UI uses its presence to render the masked
 * badge and the "fields masked: X, Y" hint.
 *
 * `failed_at` is required client-side because every list row renders
 * the relative "5m ago" timestamp — a missing value would degrade to
 * "Invalid date". The spec marks it optional; this is a local tighten
 * tracked as a spec follow-up.
 */
export type ArchivedTask = Omit<
  components['schemas']['ArchivedTask'],
  'failed_at'
> & {
  failed_at: string;
};

/**
 * The paginated list response — matches `router.Page[ArchivedTask]`
 * on the Go side.
 */
export interface DLQListResponse {
  data: ArchivedTask[];
  pagination: {
    next_cursor: string;
    prev_cursor?: string;
  };
}

/**
 * The body shape for `POST /dlq/{id}/redact`.
 *
 * Extends the spec's `RedactRequest` (which only models `fields`) with
 * the admin endpoint's `queue` discriminator. The spec will gain the
 * field once the admin DLQ surface is folded into the public schema
 * — tracked as #514 follow-up.
 */
export type RedactRequest = components['schemas']['RedactRequest'] & {
  queue: string;
};

/**
 * Common queue names for the filter chip. We don't fetch the actual
 * queue list from Asynq because it can change at runtime; the seven
 * canonical queues from the chassis cover ~all real-world traffic.
 * Operators with custom queues type-in the queue name via the URL.
 */
export const KNOWN_QUEUES: readonly string[] = [
  'critical',
  'default',
  'webhooks',
  'media',
  'search',
  'reports',
  'low',
];
