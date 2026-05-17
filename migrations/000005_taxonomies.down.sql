-- 000005_taxonomies.down.sql
--
-- Reverse of 000005_taxonomies.up.sql. Drop order is the inverse of
-- creation: triggers first (they hold function references), then the
-- functions they call, then tables in reverse-dependency order, with
-- term_relationships → terms → taxonomies.
--
-- We use CASCADE on the terms drop only for the self-FK; the
-- term_relationships drop above removes the only outside FK pointing
-- at terms, so the regular DROP is sufficient.

-- Triggers on term_relationships (defined in 000005 up).
DROP TRIGGER IF EXISTS term_relationships_recount_del ON term_relationships;
DROP TRIGGER IF EXISTS term_relationships_recount_ins ON term_relationships;

-- Triggers on terms.
DROP TRIGGER IF EXISTS terms_cascade_path_upd ON terms;
DROP TRIGGER IF EXISTS terms_set_path_upd     ON terms;
DROP TRIGGER IF EXISTS terms_set_path_ins     ON terms;

-- Tables (children → parents).
DROP TABLE IF EXISTS term_relationships;
DROP TABLE IF EXISTS terms;
DROP TABLE IF EXISTS taxonomies;

-- Functions last (after every trigger / default that references them
-- has been removed by the DROPs above).
DROP FUNCTION IF EXISTS recount_terms_on_rel_change();
DROP FUNCTION IF EXISTS terms_cascade_path();
DROP FUNCTION IF EXISTS terms_set_path();
DROP FUNCTION IF EXISTS compute_term_path(UUID, text);
DROP FUNCTION IF EXISTS term_slug_to_label(text);
