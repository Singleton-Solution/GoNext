# Database migrations

Postgres migrations live here. We use [`golang-migrate`](https://github.com/golang-migrate/migrate) (see ADR 0006 and issue #96).

## File format

Pairs of `NNNNNN_short_name.up.sql` and `NNNNNN_short_name.down.sql`. The numeric prefix is the migration version; subsequent migrations must use a higher number than any existing one.

```
000001_init.up.sql
000001_init.down.sql
000002_users.up.sql
000002_users.down.sql
...
```

## Rules

- **Forward-only after merge.** Once a migration lands on `main`, treat it as immutable. To roll back a change, write a new migration that undoes it.
- **Each PR adds migrations at the end of the sequence.** No reusing or editing existing files.
- **Down migrations are required** for PR review (to make the change reversible during development), but production rollback is handled via a new forward migration.
- **No destructive operations** without an ADR. Dropping a column or table requires the expand/contract pattern (see [doc 09 §13](../docs/09-deployment-ops.md)).
- **UUID v7 PKs only** per ADR 0003. The `gen_uuid_v7()` function must be created in the first migration.

## Running

Migrations are applied automatically on boot in dev (see issue #96). Manual application:

```bash
gonext migrate up
gonext migrate down 1
gonext migrate status
```

## Adding a migration

1. Bump to the next number: `printf "%06d" $(($(ls -1 [0-9]*.up.sql 2>/dev/null | wc -l) + 1))`
2. Create both files: `NNNNNN_my_change.up.sql` and `NNNNNN_my_change.down.sql`
3. Write the migration. Keep it small. One logical change per migration.
4. Test locally: `make up && gonext migrate up && gonext migrate down 1 && gonext migrate up`
5. Commit with a Conventional Commit message: `feat(db): add NNNNNN_my_change`

## Status

Empty — issue #96 wires up `golang-migrate`. Issues #33, #39, #48, #55, #62, #70, #77, #92, #99, #106 will add the schema migrations.
