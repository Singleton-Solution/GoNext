# ADR 0003: All tables use UUID v7 primary keys

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 01 §10 (database schema), doc 04 §1.4, doc 07 §4
- **Informed**: schema authors across all subsystems

## Context

Every table in the system needs a primary key. The conventional Postgres default for a long time has been `BIGSERIAL` (a 64-bit autoincrement integer); the modern alternative is some flavor of UUID. The choice cascades into every foreign key, every URL containing an entity ID, every cache key, every audit row, every replication concern, and every potential migration to multi-tenancy.

For GoNext specifically, the design has two non-negotiable forward commitments:

1. **Multisite forward-compatibility.** Per proposal Q00-3, every table gets a `site_id` column from day one even though v1 is single-site. The PK must be unique across sites without coordination, because in a future multi-region multi-tenant deployment we do not want to centralize ID generation.
2. **Plugin write paths.** Plugins (doc 02) get write access only to their own plugin-owned tables. Those tables share the same ID space as core tables (so plugin rows can reference core rows). A plugin in one site must never be able to predict or enumerate IDs from another plugin or another site.

UUID v4 is random — collision-free but kills index locality on hot insert paths because a new row's PK is uniformly distributed across the B-tree. UUID v7 (RFC 9562) embeds a millisecond Unix timestamp in the high bits so generated UUIDs are time-sortable: new rows cluster at the right edge of the index, exactly where `BIGSERIAL` would cluster them. ULIDs achieve the same property with a different layout, but Postgres support for ULID is weaker (no extension widely deployed; client-side generation only).

The Postgres ecosystem now has the `pg_uuidv7` extension shipping `gen_uuid_v7()` natively. Where that extension is not available (managed Postgres without the extension allowed) the design doc commits to shipping a plpgsql fallback in the base migration.

This is canonical contract **S1** in the design docs: every PK column across docs 01, 04, and 07 is `UUID PRIMARY KEY DEFAULT gen_uuid_v7()`. The cache invalidation tag vocabulary (doc 07 §16.1) is defined in terms of those UUIDs.

## Decision

Every table in the system uses `id UUID PRIMARY KEY DEFAULT gen_uuid_v7()`. Every foreign key is `UUID REFERENCES x(id) ON DELETE <explicit>`. `BIGSERIAL` is banned from the schema. The base migration installs the `pg_uuidv7` extension when permitted and a plpgsql `gen_uuid_v7()` fallback when not. UUID v7's time-sortable property is treated as load-bearing for index locality and pagination — any operation that depends on "roughly chronological" ordering is allowed to assume PKs are time-sortable.

## Consequences

### Positive

- New rows cluster at the right edge of the index, matching `BIGSERIAL` insert performance. No hot-row contention from random PKs (the UUID v4 failure mode).
- PKs are globally unique without coordination. Adding multisite later (Q00-3) does not require renumbering. Cross-site joins via plugins are safe.
- IDs are non-enumerable. `/api/posts/42` does not invite a brute-force scan; `/api/posts/01HX...` does not. Reduces a class of low-effort enumeration attacks against the public REST surface (doc 05).
- The timestamp prefix is recoverable from the UUID. Logs, audit rows, and cache keys carry implicit "when was this entity created" information without an extra column.
- Cache invalidation tags (`post:{uuid}`, `term:{uuid}` — doc 07 §16.1) are uniform across entity types. No "is this an int or a uuid" branching in the tag formatter.

### Negative

- UUIDs are 16 bytes; `BIGSERIAL` is 8. Every PK, every FK, every index entry doubles in width. For a posts table with five FKs and four indexes, that is a meaningful storage cost on a heavily-loaded site. We accept it.
- UUIDs printed in URLs and logs are 36 characters (or 22 base64), versus 1–10 for a `BIGSERIAL`. Logs are wider, URLs are uglier. Admin UI must show truncated UUIDs in tables.
- The pg_uuidv7 extension is not in core Postgres. Operators on locked-down managed Postgres without the ability to install extensions hit the plpgsql fallback, which is slower (still microseconds, but measurable in bulk insert).
- Some Postgres tooling (pg_dump-then-pg_restore for development snapshots) handles `UUID DEFAULT` values fine, but legacy ORMs and admin tools that assume integer PKs will need configuration. We do not use such tools, but plugin authors might.

### Neutral / accepted tradeoffs

- We do not expose monotonic IDs anywhere. Anything that needs "creation order" reads `created_at` or sorts by PK (time-sortable). We never expose "this is the 7,432nd post" counts.
- We do not use UUID v1 or v4 anywhere. v7 is the canonical generator. The only exception is `gen_random_uuid()` (v4) as a fallback if neither pg_uuidv7 nor our plpgsql function is present, but the base migration installs the plpgsql function so this is a theoretical fallback only.

## Alternatives considered

### Option A: `BIGSERIAL` / `BIGINT GENERATED ALWAYS AS IDENTITY`
- Rejected. Hot-row contention on the last index page under high concurrent insert. Predictable integer IDs invite enumeration of the REST surface (a documented WordPress attack class). Adding multisite later requires renumbering — the worst-case migration.

### Option B: ULID (Crockford base32, time-sortable like UUID v7)
- Rejected. Postgres-side generation requires a third-party extension less common than `pg_uuidv7`, or client-side generation that fights with the `DEFAULT` clause and trigger-based audit. The ergonomic and operational benefits over UUID v7 are negligible.

### Option C: UUID v4 (random)
- Rejected. Random insertion order destroys B-tree locality on hot insert paths. Measured impact on WordPress-shaped workloads (heavy on `posts` and `postmeta` inserts) is 2–5× slower bulk insert under load. We get no compensating benefit over v7.

### Option D: Composite keys (e.g., `(site_id, sequence_no)`)
- Rejected. Every FK widens to two columns. Joins get harder to write and harder for the planner. Adding a new ID dimension later (region, shard) requires another schema migration. UUID v7 absorbs all of that into a single 16-byte value.

### Option E: Snowflake-style 64-bit IDs (Twitter/Discord)
- Rejected. Requires a centralized or coordinated ID generator (snowflake nodes need unique node IDs). The whole appeal of UUID v7 is that any Postgres backend can generate one with zero coordination.

## References

- Design doc: `docs/01-core-cms.md` §10 (canonical contract S1)
- Design doc: `docs/04-block-editor.md` §1.4 (block tree storage)
- Design doc: `docs/07-media-performance.md` §16.1 (cache tag vocabulary)
- RFC 9562 (UUIDv6/v7/v8): https://datatracker.ietf.org/doc/rfc9562/
- pg_uuidv7 extension: https://github.com/fboulnois/pg_uuidv7
- Related ADRs: ADR 0004 (Postgres choice), ADR 0011 (cache tags)
