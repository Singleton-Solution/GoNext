/**
 * Wire types for the sessions self-service surface. Matches the JSON
 * shapes returned by /api/v1/auth/sessions (issue #205).
 */
export interface SessionView {
  /** Stable per-session hex identifier (truncated SHA-256 of token). */
  id: string;
  created_at: string;
  last_seen_at: string;
  device_label: string;
  ip: string;
  /** True iff this session is the one carrying the current request. */
  current: boolean;
}

export interface SessionListResponse {
  sessions: SessionView[];
}
