// Package taskspec is the single source-of-truth registry for every
// background-job task type GoNext runs on asynq.
//
// Why this package exists
//
// GoNext fans tasks across asynq queues: webhook.deliver,
// email.send, media.thumbnail, and the rest. Before this package each
// of those declared its name, its queue, its retry policy, its payload
// shape, and its handler in a different file — usually one per
// package — and the producer side had to coordinate with the consumer
// side by code review alone. A retry-policy change in the webhook
// package didn't propagate to the worker wiring; a renamed queue could
// silently send tasks into the void.
//
// taskspec consolidates all five facts into one TaskSpec value:
//
//	type TaskSpec struct {
//	    Name           string                                              // "webhook.deliver"
//	    Queue          string                                              // "default"
//	    MaxRetry       int                                                 // 7
//	    Timeout        time.Duration                                       // 30s
//	    PayloadSchema  *jsonschema.Schema                                  // draft 2020-12
//	    Handler        func(ctx context.Context, payload []byte) error    // unwrapped
//	}
//
// One TaskSpec describes one task end-to-end. The producer side
// (Enqueue) reads it; the consumer side (Dispatch) reads it. The two
// dereference the same struct, so drift between them is structural —
// you can't change MaxRetry in one place and forget to update the other.
//
// What's in this package
//
//   - TaskSpec — the descriptor struct above.
//   - Registry — process-wide store, safe for concurrent
//     Register / Get / Names, first-writer-wins on duplicate Name,
//     reachable as a clean instance via NewRegistry or as the
//     pre-seeded singleton via Default.
//   - Enqueue — looks up a spec by name, validates the payload against
//     PayloadSchema (jsonschemautil draft 2020-12), and calls
//     asynq.Client.EnqueueContext with the spec's Queue/MaxRetry/Timeout
//     applied. Returns the asynq.TaskInfo or a typed error
//     (ErrNilClient, ErrUnknownTask, ErrInvalidPayload).
//   - Dispatch — walks the registry and registers each spec's Handler
//     onto an asynq.ServeMux under the spec's Name, adapting the
//     (ctx, *asynq.Task) signature to (ctx, []byte) so handler
//     packages don't import asynq.
//
// Typical wiring
//
// In the package that owns a task type:
//
//	// packages/go/webhooks/delivery/spec.go
//	var Spec = taskspec.TaskSpec{
//	    Name:          "webhook.deliver",
//	    Queue:         "default",
//	    MaxRetry:      7,
//	    Timeout:       30 * time.Second,
//	    PayloadSchema: mustCompile(webhookPayloadSchema),
//	    Handler:       Handle,
//	}
//
//	func init() {
//	    if err := taskspec.Default().Register(Spec); err != nil {
//	        panic(err)
//	    }
//	}
//
// In the API server (producer):
//
//	info, err := taskspec.Enqueue(ctx, asynqClient, taskspec.Default(),
//	    "webhook.deliver", deliveryPayload)
//
// In the worker process (consumer):
//
//	mux := asynq.NewServeMux()
//	wired := taskspec.Dispatch(mux, taskspec.Default())
//	log.Info("wired tasks", "names", wired)
//	srv.Run(mux)
//
// Relationship to other registries
//
// This package follows the same shape as packages/go/policy (user
// capabilities) and packages/go/plugins/capabilities (host plugin
// capabilities): a Registry singleton, first-writer-wins, race-clean,
// sorted Names. The three registries are conceptually a triple — caps
// gate who can do what, plugin caps gate what wasm modules can do, and
// taskspecs declare the background work the system runs to fulfill
// those actions. Operators see all three in the admin UI.
package taskspec
