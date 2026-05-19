// Package webhooks implements the admin REST surface for webhook
// subscription management.
//
// The package owns four concerns:
//
//   - CRUD for the webhook_subscriptions table (#104 scaffolded the
//     in-memory store; this package adds the persistent store and
//     wires the HTTP shell).
//
//   - A "test send" endpoint that delivers a synthetic webhook.test
//     event so operators can verify reachability + signature without
//     waiting for a real event.
//
//   - A read endpoint over webhook_deliveries — the append-only audit
//     log the delivery worker (#348) writes after every attempt. The
//     admin UI shows this on the subscription detail page.
//
//   - A small EventCatalog the create form uses to populate its
//     events multi-select. The catalog is intentionally an in-package
//     constant for now (one source of truth, no separate registry to
//     keep in sync); a plugin-extensibility seam can be added later
//     without changing the HTTP contract.
//
// Capability gate: every endpoint is gated by policy.CapWebhooksManage.
// The gate is separate from jobs.admin because webhooks expose
// subscriber secrets (a created subscription's secret is returned once
// and never again).
package webhooks
