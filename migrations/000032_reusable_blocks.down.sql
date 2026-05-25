-- 000032_reusable_blocks.down.sql
--
-- Drop the reusable_blocks table.
--
-- This is destructive — every reusable block authors have created is
-- erased, and every core/block reference in posts.content_blocks
-- becomes a dangling pointer that the renderer surfaces as an
-- "Unknown block" fallback. The production rollback path is a new
-- forward migration; this down file exists for the development
-- reversibility loop only.

DROP TABLE IF EXISTS reusable_blocks;
