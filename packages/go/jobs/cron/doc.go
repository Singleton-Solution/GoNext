// Package cron is GoNext's leader-elected cron scheduler.
//
// # Why this package exists
//
// Asynq is great at running tasks once they're on a queue, but it has
// no opinion about when to put them there in the first place. The
// taskspec registry (packages/go/jobs/taskspec) describes WHAT a task
// is — its name, queue, retry, payload schema, handler. The thing it
// doesn't answer is: "fire revisions.purge every day at 03:00, plugin
// health checks every five minutes, idempotency keys hourly".
//
// In a single-replica deployment that question is trivial: a goroutine
// reads a cron table, sleeps until the next tick, calls Enqueue. The
// hard case is multi-replica. Two workers running the same goroutine
// is a double-fire bug; partitioning by hostname is brittle (a
// container restart shifts the mapping). The standard fix is leader
// election: every replica races for a short-TTL Redis lease; whichever
// one wins runs the scheduler; the rest stay idle and try again later.
// Leader transition is at most one TTL of missed-tick latency, which
// for the cron-cadenced tasks in §8 of docs/12-jobs-cron.md is fine
// (the cadences are coarser than the TTL we choose by an order of
// magnitude).
//
// # What's in this package
//
//   - CronSpec — a declarative descriptor: Name, Schedule (a robfig
//     cron expression validated at Register time), TaskName (the
//     taskspec.TaskSpec.Name to enqueue when the schedule fires),
//     and an optional Payload to send with each fire.
//   - Registry — process-wide store of CronSpecs. Same first-writer-
//     wins shape as taskspec.Registry; safe for concurrent Register
//     and read.
//   - Lease — Redis-based mutex with an Owner identity and a TTL.
//     Acquire is SET NX EX in Lua; Renew extends the TTL only if the
//     Owner still matches (compare-and-swap); Release deletes the
//     key only if the Owner still matches. The CAS pattern is the
//     reason this can't be implemented in plain go-redis calls —
//     a TOCTOU window between GET and DEL would let a stale leader
//     wipe a fresh leader's lease.
//   - Scheduler — the run-loop. Acquire the lease, fire any due
//     CronSpecs via taskspec.Enqueue, renew every TTL/3, on shutdown
//     Release. On lease-loss the scheduler stops firing and falls back
//     to the idle poll.
//
// # Wiring
//
// In a worker binary, after the asynq chassis and taskspec registry
// are wired:
//
//	cronReg := cron.NewRegistry()
//	if err := cron.SeedDefaults(cronReg); err != nil { ... }
//
//	sched, err := cron.NewScheduler(cron.Config{
//	    Redis:        rdb,                       // *redis.Client
//	    AsynqClient:  asynqClient,               // *asynq.Client
//	    TaskRegistry: taskspec.Default(),
//	    CronRegistry: cronReg,
//	    LeaseKey:     "gonext:cron:leader",      // shared across replicas
//	    LeaseTTL:     15 * time.Second,
//	    Owner:        hostnameWithReplicaID(),
//	    Logger:       logger,
//	})
//	if err != nil { return err }
//
//	go func() { _ = sched.Run(ctx) }()
//
// The Scheduler runs on every replica. Exactly one replica holds the
// lease at any moment; the rest poll for it. When the leader crashes
// or shuts down, a follower picks up the work within (LeaseTTL +
// jitter).
//
// # Why Redis (and not K8s Lease)
//
// Cron leadership has to work on K8s, on Docker Compose, on bare-metal
// systemd, and in a local dev laptop. The K8s coordination.k8s.io
// Lease object would require K8s API access and an in-cluster role for
// every worker pod, which is exactly the kind of platform-coupling we
// reject in docs/09-deployment-ops.md §16. Redis is already the queue
// backend; we already trust it for sessions, idempotency keys, and the
// outbox. Adding one more key with a 15s TTL is a free move.
//
// # Missed-tick policy
//
// We do NOT try to make up for ticks missed during a leader transition.
// If a daily 03:00 task fires while no leader exists, the 03:00 fire
// is dropped; the next one runs at 03:00 tomorrow. This matches the
// §8.4 policy from docs/12-jobs-cron.md: backlog-tolerant tasks must
// be written as "process anything older than cutoff", not "process
// what happened since the last tick". The §8 catalog is built around
// that assumption — revisions.purge sweeps by age, plugin health
// queries current state, etc.
//
// Inflight tasks are not affected by leader changes: once a fire goes
// onto a queue, asynq owns it. Leadership transitions only delay the
// NEXT fire, never abort a running handler.
package cron
