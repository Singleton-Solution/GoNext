/**
 * System Status API client.
 *
 * Thin wrapper over the shared apiRequest helper that surfaces the
 * StatusReport shape. Kept separate from the page so tests can mock
 * the fetcher independently of the React tree.
 */
import { api } from '../api-client';
import type { StatusReport } from './types';

const STATUS_PATH = '/api/v1/admin/status';

/**
 * Fetch the current System Status report. Forwards an optional
 * AbortSignal so the page can cancel an in-flight refresh when the
 * component unmounts or a newer auto-refresh tick starts.
 */
export function fetchStatusReport(signal?: AbortSignal): Promise<StatusReport> {
  return api.get<StatusReport>(STATUS_PATH, { signal });
}
