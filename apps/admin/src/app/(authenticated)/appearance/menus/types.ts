/**
 * Wire types for the navigation-menus admin surface. Mirrors the JSON
 * shapes returned by /api/v1/admin/menus.
 */
export interface Menu {
  id: string;
  slug: string;
  name: string;
  attrs?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface MenuItem {
  id: string;
  menu_id: string;
  /**
   * Dot-separated ltree-style ordering token. Examples:
   *   "001"           — first root item
   *   "001.001"       — first child of "001"
   * Sort lexicographically to get parents before children.
   */
  path: string;
  label: string;
  url: string;
  object_type?: 'post' | 'page' | 'term' | 'custom';
  object_id?: string;
  attrs?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface MenuListResponse {
  menus: Menu[];
}

export interface MenuWithItems {
  menu: Menu;
  items: MenuItem[];
}
