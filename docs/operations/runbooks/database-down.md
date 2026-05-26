# Runbook: Database down

> Status: Postgres is unreachable. Most read and all write endpoints are
> 5xx-ing. `/healthz` is still green on stateless replicas but `/readyz`
> is red.

## Symptom

- `gonext_db_up == 0` for >= 60 s on every API replica.
- A burst of `5xx` on `/api/v1/posts`, `/api/v1/users`, and every other
  DB-backed route.
- The admin UI shows the "API not available" banner on most surfaces.
- Sessions still work (Redis), so signed-in admins reach the dashboard
  but see "database unavailable" on every list view.
- `pgx: failed to connect` lines in the API server logs.

## First 5 minutes

1. **Confirm scope.** `kubectl get pods -n postgres` (or the managed
   provider's status page). Is the DB pod CrashLoopBackOff, or is the
   network path between API and DB the problem?
2. **Page the on-call DBA** if the DB itself is down. The API team
   cannot fix a corrupted WAL — escalate immediately.
3. **Confirm reads are degraded, not data loss.** Postgres replicas
   should still answer reads via the connection-string fallback if
   configured. Check `gonext_db_replica_up`.
4. **Flip the read-only flag** in the API config if writes need to be
   refused gracefully: `OPS_READ_ONLY=true` (graceful 503 with a clear
   error body) instead of letting the DB driver time out on every
   write.
5. **Announce in #incidents** with the gonext_db_up dashboard panel
   link and the current ETA.

## Mitigation

- **If primary is down + replicas are up:** promote a replica via the
  managed provider's failover button. Update `DATABASE_URL` secret to
  point at the new primary, roll API replicas.
- **If the DB is up but unreachable from API:** check the network
  policy (Kubernetes NetworkPolicy / security group). A recent
  deployment may have tightened the egress rules — roll back the
  network policy first, debug after.
- **If WAL is corrupted or the disk filled:** see also
  [`disk-full.md`](./disk-full.md). Bring the DB up read-only, take a
  snapshot, then attempt repair.
- **Worker drain:** the worker binary will keep retrying every job
  that touches the DB. Either pause Asynq (`gonext jobs drain`
  followed by re-enqueue when DB is back) or accept the retry
  pressure on Redis.

## Escalation

- **DBA on-call:** for DB-level issues (WAL corruption, replication
  lag, primary failover).
- **Platform on-call:** for network reachability or managed-service
  outages.
- **CTO + customer success:** if downtime is > 15 min during business
  hours.

## After-incident

- File a postmortem within 48 h. Required sections: timeline, root
  cause, what worked, what didn't, action items with owners + due
  dates.
- If the cause was an upgrade or migration, update
  `docs/operations/multi-region.md` with the lesson.
- Add a Prometheus alert for the failure mode if one was missing —
  every postmortem ships at least one new alert.
