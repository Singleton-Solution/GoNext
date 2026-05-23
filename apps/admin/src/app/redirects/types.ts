/**
 * Type contracts for the redirects admin surface.
 *
 * Mirrors the JSON shapes returned by /api/v1/admin/redirects in
 * apps/api/internal/admin/redirects/handler.go. Keep these in sync
 * by hand — once OpenAPI codegen lands (issue #310 follow-up), these
 * become generated types.
 */

export interface Redirect {
  id: string;
  source_path: string;
  destination_path: string;
  /** One of 301, 302, 307, 308. */
  status: number;
  is_regex: boolean;
  hit_count: number;
  last_hit_at?: string;
  created_at: string;
  created_by?: string;
}

export interface RedirectListResponse {
  data: Redirect[];
  pagination: { next_cursor: string };
}

export interface RedirectInput {
  source_path: string;
  destination_path: string;
  status: number;
  is_regex: boolean;
}

export interface RegexTestRequest {
  pattern: string;
  destination: string;
  sample_path: string;
}

export interface RegexTestResponse {
  compiles: boolean;
  error?: string;
  matches: boolean;
  captures?: string[];
  destination?: string;
}
