-- 000027_redirects.down.sql
--
-- Rolls back the redirects table and its indexes. DROP TABLE removes
-- the indexes with it; the explicit DROP INDEX statements exist for
-- symmetry with the up migration and to make the down semantics
-- explicit when read in isolation.

DROP INDEX IF EXISTS redirects_hit_count_idx;
DROP INDEX IF EXISTS redirects_kind_created_at_idx;
DROP INDEX IF EXISTS redirects_source_kind_uniq_idx;
DROP TABLE IF EXISTS redirects;
