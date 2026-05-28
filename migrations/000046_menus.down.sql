-- 000046_menus.down.sql
--
-- Drop order matters: menu_items references menus, so menu_items first.
DROP TRIGGER IF EXISTS menu_items_touch ON menu_items;
DROP TRIGGER IF EXISTS menus_touch ON menus;
DROP FUNCTION IF EXISTS menus_touch();

DROP INDEX IF EXISTS menu_items_menu_path_idx;
DROP TABLE IF EXISTS menu_items;
DROP TABLE IF EXISTS menus;
