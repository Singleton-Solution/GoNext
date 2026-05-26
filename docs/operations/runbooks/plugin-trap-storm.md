# Runbook: Plugin trap storm

> Status: A plugin is repeatedly trapping (out-of-fuel, memory limit,
> ABI violation), generating thousands of trap events per minute and
> pushing the host into a degraded state.

## Symptom

- `gonext_plugin_traps_total{slug=...}` is rising fast (> 50/s).
- One plugin slug dominates the rate.
- API p99 latency on hook-running endpoints is up 2-5x.
- The audit log is filling with `plugin.trapped` events from a single
  plugin.
- The Asynq `plugin` queue is backing up — every job from the
  offending slug fails immediately.

## First 5 minutes

1. **Identify the offender.** `gonext_plugin_traps_total` has a
   `slug` label. The dominant value is the offender.
2. **Deactivate the plugin via API** rather than CLI — the API path
   audits the action, which the CLI does not:
   ```bash
   curl -X POST $API/api/v1/plugins/<slug>/deactivate \
     -H "Cookie: sid=$SID"
   ```
   If the API itself is degraded enough that the call won't go
   through, fall back to `gonext plugin deactivate <slug>` and emit
   the audit event manually.
3. **Confirm the rate drops.** `gonext_plugin_traps_total{slug=...}`
   should stop rising within 30 s of deactivation.
4. **Drain the Asynq plugin queue's failed bucket** so we don't
   replay the offending jobs on the next restart:
   `gonext jobs failed --queue=plugin --slug=<slug> --drain`.
5. **Announce in #incidents** with the slug, version, and approximate
   number of traps observed.

## Mitigation

- **Plugin author needs notification.** Open a sev-2 issue against
  the plugin's repo with the trap rate, version, and a sample stack
  trace.
- **Quarantine the plugin** in the marketplace so other operators
  don't install the broken version. Marketplace API:
  `PATCH /api/v1/marketplace/plugins/<slug>` with `quarantined: true`.
- **Investigate the trap kind.** OOM and out-of-fuel are usually
  bad plugin code; ABI violations are usually a host/plugin version
  skew. Pull the actual trap message from a recent audit row.
- **Rollback to the previous plugin version** if the operator has
  the bundle in their plugin storage and the previous version was
  stable. The lifecycle manager supports
  `gonext plugin rollback <slug>` (issue #271, ship date dependent).

## Escalation

- **Plugin SRE:** primary owner. They drive the plugin author
  conversation and the marketplace quarantine.
- **Platform on-call:** if the trap storm is degrading the host
  binary (memory pressure, CPU saturation), they may need to
  oversize the API replicas temporarily.
- **Customer success:** if the affected plugin is in wide use, they
  need the canned response.

## After-incident

- Postmortem within 48 h.
- Add a Prometheus alert on
  `rate(gonext_plugin_traps_total{slug=...}[1m]) > 10` if one wasn't
  already firing.
- Update the plugin acceptance criteria (docs/02-plugin-system.md
  §11) if the failure mode wasn't covered by the existing trap
  budget.
- Consider raising the per-plugin fuel cap if the trap was legitimate
  work — and lowering it if it was abuse.
