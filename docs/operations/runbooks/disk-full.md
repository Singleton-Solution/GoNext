# Runbook: Disk full

> Status: A node (DB, Redis, media volume, API replica) has hit > 95%
> disk usage. Postgres refuses writes when its volume is full; media
> uploads fail; logs stop rotating.

## Symptom

- `node_filesystem_avail_bytes{mountpoint=...} / node_filesystem_size_bytes` < 0.05.
- Postgres logs: `ERROR: could not extend file "base/...": No space
  left on device`.
- Media uploads return `507 Insufficient Storage`.
- The API server log file stops growing (logs are being dropped at
  the journald layer).
- Recently: a runaway audit-log table, an Asynq dead-letter queue
  that nobody drained, or media uploads with no retention.

## First 5 minutes

1. **Identify the affected volume.** `df -h` (or the cloud provider's
   disk dashboard). Which mountpoint is full?
2. **Identify the largest consumer.** `du -sh /* | sort -h` on the
   affected node. The usual suspects:
   - `/var/lib/postgresql/data/base/...` — Postgres data.
   - `/var/lib/redis` — Redis AOF/RDB.
   - `/srv/media` — uploaded files.
   - `/var/log` — log files that didn't rotate.
3. **Free space immediately.** Different tactics per cause:
   - Postgres: `VACUUM FULL` is expensive but reclaims space;
     truncate the `audit_log` to the last 30 days if it's the
     bloat source (see retention policy in
     [`audit-corruption.md`](./audit-corruption.md) before doing
     this in haste).
   - Redis: `BGREWRITEAOF` to compact the AOF; check
     `redis-cli MEMORY DOCTOR` for outliers.
   - Media: drop unreferenced files (use `gonext media gc`).
   - Logs: `logrotate -f` or manually truncate old archives.
4. **Page the platform on-call.** Disk full is a platform
   responsibility.
5. **Announce in #incidents** with the mountpoint, % used, and the
   ETA for free space.

## Mitigation

- **Expand the volume.** Most managed providers support online
  expansion; this is the fastest path.
- **Migrate to a larger node.** Slower (requires data migration),
  but a permanent fix if the volume is at its provider-side ceiling.
- **Tighten retention.** If the bloat is `audit_log` or job history,
  the retention policy was probably too generous. Tune the
  retention windows in `/settings/privacy` (#225).
- **Prevent recurrence:** alert at 80% disk usage, not 95%. Add the
  alert to `docs/operations/alerts.md` if missing.

## Escalation

- **Platform on-call:** primary owner.
- **DBA on-call:** if Postgres is the affected service.
- **CTO:** if the only mitigation is "wait for a larger node, which
  takes hours" and impact is customer-visible.

## After-incident

- Postmortem within 48 h. Mandatory section: "Why didn't the 80%
  alert fire?" (Was it missing? Misrouted? Acknowledged-and-forgotten?)
- Add or fix the alert.
- Set a calendar reminder to review disk growth quarterly.
