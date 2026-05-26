/**
 * Themes admin API types — mirror the shape returned by
 * apps/api/internal/admin/themes.
 */

/** One row in the GET /api/v1/admin/themes response. */
export interface ThemeInfo {
  slug: string;
  title: string;
  description?: string;
  version: number;
  has_screenshot: boolean;
}

export interface ThemesListResponse {
  themes: ThemeInfo[];
  active_slug: string;
}

export interface InstallResponse {
  slug: string;
  title: string;
  version: number;
}
