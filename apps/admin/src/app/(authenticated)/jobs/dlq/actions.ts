/**
 * DLQ admin — server actions.
 *
 * Each helper wraps a single endpoint on the admin API. Returning the
 * parsed body (or `void` for delete-style actions) keeps the component
 * layer free of `fetch`-shaped detail.
 *
 * Why a plain module and not `'use server'` Next.js actions:
 *  - The admin API is the source of truth for ALL state. A Server
 *    Action would have to re-export the same fetch wrapper. Skipping
 *    the indirection makes the call site obvious to anyone reading the
 *    UI for the first time.
 *  - Tests stub the underlying `api` client by importing it via the
 *    same path the actions use — no Next.js test harness magic
 *    required.
 */
import { api } from '@/lib/api-client';
import type { ArchivedTask, DLQListResponse, RedactRequest } from './types';

export interface ListParams {
  queue: string;
  limit?: number;
  cursor?: string;
}

/**
 * Fetch a page of archived tasks. Cursor is opaque; the next page's
 * cursor lives in `pagination.next_cursor` of the response — pass it
 * back verbatim. The function builds the query string itself so
 * callers don't have to remember the parameter names.
 */
export function listArchivedTasks(params: ListParams): Promise<DLQListResponse> {
  const search = new URLSearchParams();
  search.set('queue', params.queue);
  if (params.limit !== undefined) {
    search.set('limit', String(params.limit));
  }
  if (params.cursor) {
    search.set('cursor', params.cursor);
  }
  return api.get<DLQListResponse>(`/api/v1/admin/jobs/dlq?${search.toString()}`);
}

/**
 * Fetch a single archived task by ID. `queue` is required because the
 * Asynq inspector keys mutations on (queue, id); the UI knows the
 * queue from the row it was rendered from.
 */
export function getArchivedTask(
  id: string,
  queue: string,
): Promise<ArchivedTask> {
  const search = new URLSearchParams({ queue });
  return api.get<ArchivedTask>(
    `/api/v1/admin/jobs/dlq/${encodeURIComponent(id)}?${search.toString()}`,
  );
}

/**
 * Replay an archived task — moves it back onto the pending queue. The
 * promise resolves with the action acknowledgement; the UI's job is to
 * refresh the list (or remove the row optimistically).
 */
export function replayTask(id: string, queue: string): Promise<unknown> {
  const search = new URLSearchParams({ queue });
  return api.post(
    `/api/v1/admin/jobs/dlq/${encodeURIComponent(id)}/replay?${search.toString()}`,
  );
}

/**
 * Discard an archived task — permanently deletes it from Redis. The
 * caller should confirm with the user first; the API does not.
 */
export function discardTask(id: string, queue: string): Promise<unknown> {
  const search = new URLSearchParams({ queue });
  return api.post(
    `/api/v1/admin/jobs/dlq/${encodeURIComponent(id)}/discard?${search.toString()}`,
  );
}

/**
 * Apply a redaction mask to a task. The next time the list or detail
 * endpoint is hit for this task, the named fields are returned as
 * `***REDACTED***`. Idempotent: re-issuing with a different field set
 * replaces the previous record (the API does not merge).
 */
export function redactTask(
  id: string,
  body: RedactRequest,
): Promise<unknown> {
  return api.post(
    `/api/v1/admin/jobs/dlq/${encodeURIComponent(id)}/redact`,
    body,
  );
}
