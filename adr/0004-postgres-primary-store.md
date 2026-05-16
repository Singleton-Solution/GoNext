# ADR 0004: PostgreSQL 15+ is the primary store

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 00 §2 (stack decisions), doc 01 (core CMS data model)
- **Informed**: schema authors, plugin SDK authors

## Context

GoNext stores content (posts, blocks, taxonomies, comments, users, media metadata, plugin tables, audit logs) in a relational database. The choice of database is load-bearing for almost every other architectural decision in the design: it picks the JSON model (doc 01 §3 / doc 04 §1), the full-text search story (doc 01 §8), the hierarchy primitive (`ltree` for term trees, doc 01 §2.2), the extension surface for UUID v7 (ADR 0003), the row-level security path for v2 multi-tenancy (proposal Q01-8), and the connection-pooling shape for plugin DB access (doc 02 §6.2).

Four databases were in scope:

- **MySQL / MariaDB.** WordPress's home. The ecosystem case for MySQL is "every WP host runs it." We are not a WP host and we are not running WP code, so the network effect is irrelevant. Technically, MySQL's JSON support is weaker than Postgres JSONB (no GIN-style functional indexes on arbitrary JSON paths without virtual columns, slower path queries, narrower operator vocabulary). MySQL full-text search is weaker than Postgres `tsvector` (smaller ranking knobs, no dictionary plumbing). MariaDB has caught up on some fronts but the calculus has not changed.
- **PostgreSQL.** Best-in-class JSONB, mature `tsvector` full-text search with per-language dictionaries, the `ltree` and `citext` and `pg_trgm` extensions that the design depends on, and the operational story that every cloud provider supports it. The `pg_uuidv7` extension (ADR 0003) is Postgres-only. Row-level security policies (deferred to v2 multi-tenant) are Postgres-only.
- **SQLite.** Tempting for the "single binary + a file" deploy story. Falls down on concurrent writers (one-writer-at-a-time semantics) and on the design's reliance on Postgres-specific extensions (`ltree`, `tsvector`, `pg_uuidv7`, `citext`). The simplicity benefit evaporates the moment two admin users save at the same time, or the cache invalidation outbox worker (doc 07 §16, ADR 0011) tries to drain rows concurrent with mutations.
- **CockroachDB.** Wire-protocol Postgres, horizontally scaled, multi-region by default. Solves problems we do not have (no global tenant in v1, no horizontal write scale required for the WP-shaped workload v1 targets). Brings cost (operational complexity, licensing for the enterprise tier) and behavioural differences (transaction model, retry semantics) that we do not want to absorb on day one.

The design docs treat Postgres-specific features as load-bearing: doc 01 §10 explicitly requires `pgcrypto`, `pg_uuidv7`, `pg_trgm`, `ltree`, `citext`, `btree_gin`. Doc 01 §8 builds FTS on `tsvector` with weighted ranks. Doc 07 §15-16 uses transactional outbox semantics that rely on Postgres's transactional DDL and `SELECT ... FOR UPDATE SKIP LOCKED` for safe worker draining. None of those features port to MySQL cleanly.

## Decision

PostgreSQL **15 or later** is the primary store for all GoNext data. We rely on the extensions listed in doc 01 §10.1 (`pgcrypto`, `pg_uuidv7`, `pg_trgm`, `ltree`, `citext`, `btree_gin`) and on the `tsvector` full-text search subsystem. We commit to writing Postgres-specific SQL where it yields meaningfully better performance or expressiveness than the lowest-common-denominator dialect; portability to other relational stores is **not a goal**.

## Consequences

### Positive

- The design docs (01, 04, 07, 06, 10, 12) work as written. Every feature that depends on a Postgres extension is on a stable foundation.
- JSONB gives us a flexible metadata story (doc 01 §3) and a block-tree storage shape (doc 04 §1.4) that would require schema gymnastics in MySQL. We can index inside JSON via GIN; we can query for blocks by type without parsing HTML; we can support custom fields without an EAV anti-pattern.
- `tsvector` FTS (doc 01 §8) gives us per-language search out of the box. Replacing it with Meilisearch or Typesense (v2 per doc 00 §2) is an additive change, not a forced migration.
- Postgres 15 ships with `MERGE`, improved partitioning, faster sorts, and the logical replication features the v2 read-replica story relies on (doc 07 §17.2).
- One database engine to know, document, monitor, back up, and tune. Plugin authors only learn one SQL dialect.

### Negative

- We lock ourselves out of MySQL-only hosts. A non-trivial subset of cheap shared hosting is MySQL-only. We accept that loss — those hosts are not our target deployment.
- Postgres operational complexity is higher than SQLite's (a database server is more to operate than a file). The development experience must still feel like "git clone, docker compose up" — we accept the docker-compose dep.
- Postgres major version upgrades require a real plan (pg_upgrade, logical replication, or dump/restore). MySQL is in the same boat; SQLite is not. We will document the upgrade path and ship a CLI helper.
- Postgres connection costs are real (per-connection memory). The plugin DB isolation model (doc 02 §6.2) creates one Postgres role per plugin and may want to layer pgbouncer in front, as flagged in proposal Q02-2.

### Neutral / accepted tradeoffs

- Some hosted Postgres providers restrict the set of extensions operators can install. The base migration installs a plpgsql `gen_uuid_v7()` fallback if `pg_uuidv7` is unavailable (ADR 0003); we will do the same for `ltree` and `citext` where feasible, or document the requirement clearly.
- We do not pursue MySQL compatibility through a translation layer. Every previous CMS that has tried this has either dropped one engine or accepted a feature-floor that satisfies neither.
- We accept that this decision is hard to reverse. The bet is that Postgres is the right long-term home for content-shaped workloads.

## Alternatives considered

### Option A: MySQL / MariaDB
- Rejected. WordPress legacy is the only meaningful ecosystem argument, and we explicitly do not run WP plugins. JSON, FTS, and extension stories are all weaker than Postgres for our specific workloads.

### Option B: SQLite
- Rejected. The concurrent-writers limit breaks under realistic admin load (multiple editors saving concurrent, cache invalidation worker draining the outbox while mutations land). The design depends on extensions SQLite does not have.

### Option C: CockroachDB
- Rejected. Horizontal write scale is not a v1 requirement; the operational cost is real; transaction semantics differ from vanilla Postgres in ways that would force test-matrix splits in code we share between deployments. Worth revisiting if multisite scales beyond what a single Postgres primary can serve.

### Option D: A dual-backend abstraction (Postgres + MySQL via an ORM)
- Rejected. Writing to the lowest-common-denominator SQL kills the things that make Postgres worth choosing (JSONB, `tsvector`, extensions). We would carry two test suites, two performance profiles, and two operational stories for marginal benefit.

### Option E: Two stores (Postgres for relational, separate document DB for JSON)
- Rejected. Postgres JSONB is good enough that splitting into a separate document store buys nothing and costs a two-phase-commit story we do not want. The block tree lives in JSONB inside `posts` (doc 04 §1.4).

## References

- Design doc: `docs/00-architecture-overview.md` §2 (stack table)
- Design doc: `docs/01-core-cms.md` §10 (schema and extensions)
- Design doc: `docs/01-core-cms.md` §8 (FTS via tsvector)
- Design doc: `docs/07-media-performance.md` §15-17 (caching, connection pooling)
- Related ADRs: ADR 0003 (UUID v7), ADR 0008 (JSONB block tree), ADR 0011 (cache invalidation outbox)
