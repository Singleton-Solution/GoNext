# Runbook: OOM

> Status: One or more API or worker replicas is being killed by the
> kernel OOM-killer. The pod restarts, holds for a few minutes, then
> dies again.

## Symptom

- `kube_pod_container_status_restarts_total{pod="api-..."}` is
  rising.
- `kubectl describe pod api-...` shows `OOMKilled` as the last
  termination reason.
- API p99 latency spikes during each restart cycle.
- `dmesg` on the node shows the OOM-killer killing the gonext-api
  process.
- The Go runtime's heap (`go_memstats_heap_inuse_bytes`) is at or
  near the container's memory limit.

## First 5 minutes

1. **Confirm scope.** Are all replicas OOMing (a code / load issue),
   or a single replica (a bad node)?
2. **If single replica:** drain it. The replacement pod gets fresh
   memory; the bad node's memory issues are someone else's problem
   (or a flaky DIMM).
3. **If all replicas:** the binary is using more memory than the
   container allows. Two possible causes:
   - A recent deploy increased the working set (new feature, larger
     cache, leak).
   - Load has grown organically and the limit hasn't.
4. **Bump the container memory limit by 50%** as a temporary measure.
   This buys time to diagnose without an outage.
5. **Page the platform on-call.**

## Mitigation

- **If a recent deploy is the cause:** roll back. The memory
  regression is the bug; fix it on a follow-up, not at 3 AM.
- **If organic load is the cause:** horizontally scale (add
  replicas) before vertically scaling (more memory per pod). The
  Go runtime is happiest with smaller, replicated instances.
- **Profile the leak.** `go tool pprof
  $API/debug/pprof/heap` (exposed only on internal `:9090`, never
  the public port). Look for an unbounded slice in a hot path.
- **Tune GOMEMLIMIT.** The Go runtime is more aggressive about
  collection when GOMEMLIMIT is set near the container limit; this
  reduces the chance of an OOM even at the same working set.
- **Worker binary specifically:** check `gonext jobs queue` for a
  task type with abnormal payload size. One bad job that pulls a
  multi-megabyte blob into memory will OOM a worker.

## Escalation

- **Platform on-call:** primary owner.
- **Owner of the suspected code change:** to triage the regression.
- **CTO:** if the OOM cycle continues for > 30 min and customer
  impact is sustained.

## After-incident

- Postmortem within 48 h.
- Capture the heap profile from the affected replica and attach it
  to the postmortem.
- If the cause was a leak, file a bug with the profile attached
  and assign to the relevant team.
- Update the deploy checklist: "did this change increase the
  working set? confirm with a benchmark before rollout."
