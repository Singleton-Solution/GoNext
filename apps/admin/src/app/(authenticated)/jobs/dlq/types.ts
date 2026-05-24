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
 */

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
 */
export interface ArchivedTask {
  id: string;
  queue: string;
  type: string;
  payload_preview: string;
  /** Base64-encoded raw bytes on the detail endpoint; omitted on list. */
  payload?: string;
  last_error: string;
  failed_at: string;
  retried: number;
  max_retry: number;
  redacted: boolean;
  redacted_fields?: string[];
}

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
 */
export interface RedactRequest {
  queue: string;
  fields: string[];
}

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
