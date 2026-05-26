# Runbook: Redis down

> Status: Redis is unreachable. Sessions, rate limits, and the job
> queue are all impacted. Postgres is fine, so cached reads degrade
> but the canonical data is intact.

## Symptom

- `gonext_redis_up == 0` for >= 60 s.
- Logged-in admins are bounced to `/login` because session lookups
  fail (cookies present, but the manager returns `ErrNotFound`).
- Login attempts fail closed — the rate-limiter cannot count buckets
  so the login handler returns 503 rather than risk an unrate-limited
  surface.
- Asynq workers fall over with `dial tcp: connection refused`.
- `gonext_jobs_inflight` drops to 0 and stays there.

## First 5 minutes

1. **Confirm scope.** Managed Redis status page, or
   `kubectl get pods -n redis`. Is it the pod, the network, or the
   client config?
2. **Check session blast radius.** Every signed-in admin is logged
   out the moment their request lands on a node that can't reach
   Redis. Public traffic (theme reads, RSS) is unaffected.
3. **Page the on-call platform engineer.** Redis is shared between
   sessions, rate limits, and the queue — a single-node outage
   affects three independent subsystems.
4. **Pause the worker binary.** `kubectl scale deploy/worker
   --replicas=0` so we don't generate Asynq reconnection storm noise
   while Redis recovers.
5. **Announce in #incidents** with the impact summary (sessions out,
   queue paused, logins refused).

## Mitigation

- **Failover to a Redis replica** if the cluster is configured for
  HA. Update `REDIS_URL` secret to the new primary, roll API + worker
  replicas.
- **If sessions need to come back ASAP and Redis is unrecoverable:**
  point `REDIS_URL` at a fresh empty Redis. Existing sessions are
  lost (everyone re-authenticates), but the admin is usable. Coordinate
  with comms before doing this — it's user-visible.
- **If only the queue is needed:** stand up a temporary Redis for
  Asynq while the original recovers, dual-write nothing (Asynq is
  stateful — running two clusters simultaneously corrupts task
  ordering).
- **Rate-limit fallback:** the limiter falls open during a Redis
  outage, which means an attacker hitting `/api/v1/auth/login` is
  unbounded. Mitigate via the WAF or cloud-side IP rate limit
  until Redis returns.

## Escalation

- **Platform on-call:** primary owner. Redis infra is theirs.
- **Security on-call:** if the outage exceeds 10 min during business
  hours — they need to know the rate-limit gate is open.
- **CTO:** if customer-visible session loss exceeds 30 min.

## After-incident

- Postmortem within 48 h.
- Capture how many users were logged out (sum of `Set-Cookie:
  sid=; Max-Age=0` from the lb logs).
- Add a Prometheus alert for `gonext_redis_up == 0` for > 30 s if
  one wasn't already firing.
- If the cause was an upgrade, document the lesson in the deploy
  checklist.
