// Package outbox implements the transactional outbox pattern for
// at-least-once Redis-job delivery.
//
// Problem
//
// A handler that does
//
//	tx.Commit()
//	redis.Enqueue(...)
//
// can crash between the two statements. The DB change is durable but
// the job never fires — the system is inconsistent and there's no
// automatic recovery path.
//
// Solution
//
// Treat the queue write as an ordinary database write:
//
//  1. The handler calls outbox.Store.Write(ctx, tx, entry) *inside*
//     its own transaction. The row is committed (or rolled back)
//     atomically with the rest of the handler's writes.
//
//  2. A separate Poller process loops:
//     a. Claim N unclaimed rows under FOR UPDATE SKIP LOCKED.
//     b. Enqueue each to Redis via the supplied Enqueuer.
//     c. Delete the row on success; release the claim + bump
//        attempts on failure.
//
//  3. A periodic recovery sweep releases rows whose lease has
//     expired (stuck workers).
//
// Guarantees
//
//   - DB-side correctness: if the handler's transaction commits, the
//     row exists in outbox; if it rolls back, the row doesn't exist.
//     There is no path where the application "did work and forgot to
//     enqueue".
//
//   - At-least-once delivery: a poller may crash after Redis acks the
//     enqueue but before deleting the row, so a downstream worker
//     must be idempotent. This is the standard outbox-pattern
//     trade-off.
//
//   - Concurrent pollers are safe: FOR UPDATE SKIP LOCKED guarantees
//     that two pollers will never claim the same row.
//
// Reference: Hohpe + Woolf, "Guaranteed Delivery"; the microservices
// transactional-outbox pattern.
package outbox
