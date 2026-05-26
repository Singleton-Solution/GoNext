# Runbook: Audit log corruption

> Status: The `audit_log` table is unreadable, has gaps, or its hash
> chain has broken. This is a **compliance-relevant** incident — the
> audit log is the source of truth for "who did what, when", and a
> gap is a finding on every SOC 2 audit.

## Symptom

- `gonext_audit_emit_failures_total` is non-zero and rising.
- `gonext audit tail` returns rows with `prev_hash != sha256(prev_row)`,
  i.e. the tamper-evidence chain is broken.
- Admins report "I revoked X yesterday but the audit log doesn't show
  it."
- Postgres errors: `ERROR: invalid page in block N of relation
  audit_log`.
- A migration that backfilled `audit_log` failed mid-way.

## First 5 minutes

1. **Stop the bleeding.** If audit writes are failing, every
   privileged action is happening UNAUDITED. Either:
   - flip the API to read-only via `OPS_READ_ONLY=true`, or
   - point the audit emitter at an emergency in-memory buffer
     (`AUDIT_EMERGENCY_BUFFER=true`) so writes are queued for replay
     when Postgres recovers.
2. **Page the security on-call.** This is their incident, not the
   platform team's — the audit log is a security artifact.
3. **Snapshot the current `audit_log`.** Before anyone runs a repair,
   `pg_dump --table=audit_log` to a write-locked bucket. This is the
   evidence chain.
4. **Disable the sweeper.** The retention sweeper is going to make
   the gap unrecoverable if it runs during the incident. Set
   `AUDIT_SWEEPER_DISABLED=true` and roll API replicas.
5. **Announce in #incidents AND #compliance.**

## Mitigation

- **If the corruption is at the storage level** (Postgres page
  error): see also [`database-down.md`](./database-down.md). Restore
  the table from the most recent backup that predates the corruption.
- **If the chain broke because a row was manually edited:** the
  table is now unauditable for everything after that row. Document
  the gap (with row IDs) in the postmortem and treat it as a finding.
  Do not attempt to "re-link" the chain by recomputing hashes — that
  destroys the tamper evidence.
- **If a backfill migration is the cause:** roll the migration back,
  restore the pre-migration `audit_log` snapshot, replay the
  emergency buffer if one was captured.
- **Re-enable normal writes** only after security on-call signs off
  that the chain is consistent again.

## Escalation

- **Security on-call:** primary owner. They drive comms with
  compliance.
- **DBA on-call:** for the Postgres-level repair.
- **Customer success:** if any customer asks about an audit gap —
  they need the canned response.

## After-incident

- Postmortem within 24 h (faster than normal because it's
  compliance-relevant).
- Append a "gap" record to the audit chain documenting the affected
  time range and root cause.
- Update the compliance binder with the incident reference.
- If the cause was a code path that wrote to `audit_log` outside the
  emitter, fix it and add a CI gate.
