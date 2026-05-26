# Runbook: Mass 401s

> Status: Every authenticated request is getting `401 Unauthorized`.
> Either the session store is broken, the cookie domain is misconfigured,
> or a signing-key rotation went wrong.

## Symptom

- `gonext_http_requests_total{status="401"}` spikes from baseline
  (< 1%) to > 30% over a 1-2 minute window.
- Admin users report "I keep getting logged out."
- The admin UI shows the login screen even right after a successful
  login (the cookie comes back but the next request rejects it).
- `auth.session.not_found` audit events spike.

## First 5 minutes

1. **Confirm scope.** Is it ALL users (likely a session store or
   cookie-domain problem) or just some (likely a per-user state
   issue)?
2. **Check Redis health.** See [`redis-down.md`](./redis-down.md) —
   a Redis outage manifests as mass 401 because every session lookup
   fails.
3. **Check the cookie config.** A recent deploy that changed
   `Cookie-Domain`, `SameSite`, or `Secure` flag will silently
   invalidate every existing session. Compare the cookie attributes
   on the current response to a known-good capture from earlier in
   the day.
4. **Check for signing-key rotation.** If the auth layer uses HMAC
   over the session token (it does, per docs/06-auth-permissions.md
   §5), rotating the signing key without a grace window invalidates
   every session. Look for a recent secret rotation in the audit log.
5. **Page the auth on-call.**

## Mitigation

- **If Redis is the cause:** follow [`redis-down.md`](./redis-down.md).
- **If a cookie attribute change is the cause:** revert the deploy.
  Existing sessions become valid again immediately.
- **If a key rotation is the cause:** restore the previous signing
  key alongside the new one (the auth layer supports a key-set so
  you can have both active during rotation). Sessions minted under
  the old key validate again. Roll the new key out the next deploy
  with the key-set logic in place.
- **Worst case — accept the session loss:** announce "you may need
  to log in again" via the in-app banner, let users re-authenticate.
  Only acceptable if the root cause is fixed and recurrence is
  unlikely.

## Escalation

- **Auth on-call:** primary owner.
- **Platform on-call:** if the cause is upstream (LB, WAF) stripping
  cookies.
- **Customer success:** if the impact lasts > 10 min during business
  hours.

## After-incident

- Postmortem within 48 h.
- If the cause was a config change, add a CI check that flags
  changes to the cookie envelope as a "session-invalidating change"
  requiring two reviewers.
- If the cause was a key rotation without grace window, document
  the rotation procedure in `docs/operations/multi-region.md` (and
  in the runbook for the rotation itself).
