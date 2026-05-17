// Package delivery is the GoNext webhook outbound delivery worker.
//
// The webhook subsystem decomposes into two halves:
//
//   - The "control plane" — subscription CRUD, the event catalog, the
//     admin UI surface, signing-secret management. Owned by doc 05 and
//     a sibling package (packages/go/webhooks/) in the broader package
//     layout. Issues that follow add it.
//
//   - The "data plane" — the worker task that takes a queued event and
//     POSTs it to the subscriber URL with an HMAC signature, retries on
//     transient failure, and dead-letters on persistent failure. That's
//     this package. See docs/12-jobs-cron.md §14 for the mechanics
//     contract this package implements.
//
// The split keeps the delivery worker focused: it consumes the queued
// payload and the subscription metadata as inputs, runs one HTTP attempt,
// and returns one of {ok, retry, deadletter}. It does not know how the
// payload got onto the queue, where the signing secret comes from
// (anything implementing SecretResolver), or how the subscription is
// persisted (anything implementing Subscriptions). That makes the worker
// testable end-to-end against an httptest.Server with zero external
// dependencies, and lets the broader webhooks package wire it up later
// without re-opening this code.
//
// Wire-format compatibility:
//
//   - The HMAC signature uses the Stripe-style format
//     `X-GoNext-Signature: t=<unix>,v1=<hex>` where `v1 = HMAC-SHA256(secret,
//     timestamp + "." + body)`. Subscribers should compare with a constant-
//     time comparison and additionally enforce a max-skew window to defeat
//     replay. The package's Verify helper does both.
//
//   - `X-GoNext-Event-Id` is constant across attempts so subscribers can
//     dedupe on it. `X-GoNext-Delivery-Id` and `X-GoNext-Attempt` are per-
//     attempt and let subscribers correlate logs.
//
// Retry policy: a fixed schedule with full jitter, defaults
// {30s, 5m, 30m, 2h, 6h, 24h}. After the schedule is exhausted (6 failures
// in the default) Deadletter records an audit event and asks the
// subscription store to mark the subscription degraded. Both behaviours
// are pluggable; the unit tests exercise the contract without either side
// hitting a real backend.
//
// HTTP client: a single net/http.Client per Deliverer with a hardened
// transport — connect 5s, overall 30s (configurable), TLS verification
// on, redirect chain capped at 0 (subscribers must publish a stable URL
// rather than redirecting via a third party). The redirect cap is the
// same defense the GitHub and Stripe webhook deliverers use: a redirect
// would let a misconfigured subscriber re-emit our signed body to an
// unrelated host.
//
// Concurrency: Deliverer is safe for concurrent Deliver calls. There is
// no internal state shared across calls beyond the (already-safe) http
// client and the (caller-provided) stores.
package delivery
