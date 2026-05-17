package delivery

import (
	"context"
	"errors"
	"time"
)

// Payload is the unit of work for one webhook delivery attempt.
//
// SubscriptionID + EventID together form the idempotency key — a task
// re-enqueued for the same pair never delivers twice. The body is opaque
// bytes; this package does not parse it.
//
// Headers are user-supplied custom headers the subscription stored
// (Stripe-style "give me an extra X-Tenant: foo on my webhooks"). The
// package's reserved headers (X-GoNext-Signature, X-GoNext-Event-Id,
// X-GoNext-Delivery-Id, X-GoNext-Timestamp, X-GoNext-Attempt,
// Content-Type) take precedence over anything the caller smuggles in.
type Payload struct {
	// SubscriptionID identifies which subscription this delivery serves.
	// Required.
	SubscriptionID string

	// EventID is the immutable identifier for the event being delivered.
	// Constant across retry attempts so subscribers can dedupe. Required.
	EventID string

	// EventType is the dotted event name (e.g. "post.published"). Echoed
	// back as X-GoNext-Event-Type for subscriber convenience.
	EventType string

	// Body is the raw serialized event bytes that go over the wire.
	// Required.
	Body []byte

	// Headers are subscription-specific extras (typically configured by
	// the subscriber owner via the admin UI). Reserved X-GoNext-* and
	// Content-Type headers are protected against override.
	Headers map[string]string

	// Attempt is 1-based and increments per retry. The first delivery
	// attempt is 1, not 0. Used in the X-GoNext-Attempt header and as
	// the index into the retry schedule for the *next* attempt.
	Attempt int
}

// Subscription is the minimal slice of subscription state the deliverer
// needs to run one attempt. The full subscription record (owner, event
// filter, created_at, …) lives in the control-plane package; this type
// is the contract.
type Subscription struct {
	// ID is the subscription's stable identifier. Used in logs/audit
	// rows and in the X-GoNext-Subscription-Id header.
	ID string

	// URL is the absolute https:// (or http:// in dev) endpoint to POST
	// to. The deliverer enforces scheme-allowlist via the optional
	// AllowInsecureScheme setting (default: only https is allowed).
	URL string

	// SecretID names the signing secret. The Deliverer's SecretResolver
	// translates this into the raw HMAC key bytes at delivery time. The
	// indirection keeps the raw secret out of payloads, audit logs, and
	// DLQ dumps; rotation is a SecretResolver concern, not a queue
	// concern.
	SecretID string
}

// Result is the outcome of one Deliver call.
//
// The worker (Asynq handler, in the production wiring) maps this to the
// queue's verbs: Success → return nil, Retry → return a transient error
// for the retry policy to schedule, Deadletter → return SkipRetry to send
// the task to the archive immediately.
type Result struct {
	// Status is one of StatusSuccess, StatusRetry, StatusDeadletter.
	Status Status

	// HTTPStatus is the subscriber's HTTP status code, or 0 if the
	// request never produced a response (DNS failure, timeout, TLS
	// error). Used for telemetry and for the dead-letter audit row.
	HTTPStatus int

	// Latency is how long the request spent in the HTTP client, from
	// just-before-Do to just-after-the-response-was-closed. Zero on
	// errors that prevented the request from being sent.
	Latency time.Duration

	// Attempt echoes the attempt number that produced this result.
	Attempt int

	// NextDelay is populated on StatusRetry: how long the caller should
	// wait before re-enqueueing. Honors Retry-After when the subscriber
	// supplied it (capped against the schedule's value to keep a
	// pathological subscriber from pushing retries out indefinitely).
	NextDelay time.Duration

	// Err carries the failure cause for StatusRetry/StatusDeadletter and
	// is nil on StatusSuccess. The error is wrapped with the standard
	// errors.Is targets in this package so callers can branch on
	// classification without parsing strings.
	Err error
}

// Status enumerates Deliver outcomes.
type Status int

const (
	// StatusSuccess is a 2xx response within the timeout.
	StatusSuccess Status = iota

	// StatusRetry is a transient failure: 5xx, 408, 429, timeout, TCP
	// reset, DNS hiccup. The task should be re-enqueued with NextDelay.
	StatusRetry

	// StatusDeadletter is a permanent failure: a 4xx that won't get
	// better, a refused scheme, or the retry schedule has been
	// exhausted. The task should be archived; the subscription is
	// marked degraded by the configured DeadletterRecorder.
	StatusDeadletter
)

// Sentinel errors. Returned wrapped via errors.Join / fmt.Errorf("%w") so
// callers can branch on classification with errors.Is.
var (
	// ErrPermanent indicates a failure that will not improve with retry
	// (e.g. 410 Gone, 400 Bad Request, scheme not allowed).
	ErrPermanent = errors.New("webhook delivery: permanent failure")

	// ErrTransient indicates a failure that may improve with retry
	// (5xx, timeout, transport error).
	ErrTransient = errors.New("webhook delivery: transient failure")

	// ErrExhausted indicates the retry schedule has been used up.
	ErrExhausted = errors.New("webhook delivery: retry schedule exhausted")

	// ErrInvalidPayload indicates the caller-supplied Payload is
	// structurally invalid (missing required field). Treated as
	// permanent — a poison payload should not be retried forever.
	ErrInvalidPayload = errors.New("webhook delivery: invalid payload")

	// ErrInvalidSubscription indicates the caller-supplied Subscription
	// is structurally invalid (missing URL or SecretID, or URL uses a
	// disallowed scheme).
	ErrInvalidSubscription = errors.New("webhook delivery: invalid subscription")
)

// SecretResolver returns the raw HMAC key bytes for a secret reference.
//
// Implementations typically read from a secrets package; the indirection
// keeps the raw bytes out of every layer above the actual sign() call.
// The bytes returned MUST NOT be retained by the caller — Deliverer
// passes them straight to hmac.New and discards.
type SecretResolver interface {
	Resolve(ctx context.Context, secretID string) ([]byte, error)
}

// SecretResolverFunc adapts a function to SecretResolver. Useful in tests.
type SecretResolverFunc func(ctx context.Context, secretID string) ([]byte, error)

// Resolve implements SecretResolver.
func (f SecretResolverFunc) Resolve(ctx context.Context, secretID string) ([]byte, error) {
	return f(ctx, secretID)
}

// IdempotencyStore prevents the same (SubscriptionID, EventID) pair from
// being delivered more than once even if the task is re-enqueued.
//
// Claim returns (true, nil) when the caller has just won the right to
// deliver this pair (a fresh claim was written). It returns (false, nil)
// when a prior claim is still live — the caller MUST treat the task as
// success without sending a second HTTP request.
//
// Implementations are typically a Redis SET NX EX with the
// (subscription, event) tuple as the key. The TTL bounds memory; the
// docs/12-jobs-cron.md §7 catalog says 7 days for webhook.deliver.
//
// A nil IdempotencyStore on the Deliverer disables idempotency checks
// (every Deliver attempts a send). This is the safe default for tests
// that exercise the retry path without involving Redis. Production
// wiring should always set one.
type IdempotencyStore interface {
	Claim(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// IdempotencyStoreFunc adapts a function to IdempotencyStore. Useful in
// tests where the store is a fake map.
type IdempotencyStoreFunc func(ctx context.Context, key string, ttl time.Duration) (bool, error)

// Claim implements IdempotencyStore.
func (f IdempotencyStoreFunc) Claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return f(ctx, key, ttl)
}

// Subscriptions is the slice of the subscription store that the
// dead-letter pipeline needs: it must be able to mark a subscription as
// degraded after the retry schedule is exhausted (or after we hit a
// permanent 4xx that proves the URL is dead).
//
// The Deliverer never reads from this interface, only writes — reading
// subscriptions is the caller's job (typically done at enqueue time,
// then the Payload + Subscription pair is handed to Deliver).
type Subscriptions interface {
	// MarkDegraded records that the subscription has accumulated enough
	// failures that the operator should look at it. `reason` is a short
	// machine-readable tag ("schedule_exhausted", "permanent_4xx",
	// "url_gone"); details are in the audit row.
	MarkDegraded(ctx context.Context, subscriptionID, reason string) error
}

// SubscriptionsFunc adapts a function to Subscriptions.
type SubscriptionsFunc func(ctx context.Context, subscriptionID, reason string) error

// MarkDegraded implements Subscriptions.
func (f SubscriptionsFunc) MarkDegraded(ctx context.Context, subscriptionID, reason string) error {
	return f(ctx, subscriptionID, reason)
}

// AuditRecorder is the slice of the audit emitter the deadletter pipeline
// needs. We accept an interface (rather than *audit.Emitter directly) so
// the package has no compile-time dependency on the audit module — tests
// pass a fake.
//
// The production wiring will pass an adapter that calls
// audit.Emitter.Emit with the appropriate event type and metadata.
type AuditRecorder interface {
	RecordDeadletter(ctx context.Context, evt DeadletterEvent) error
}

// AuditRecorderFunc adapts a function to AuditRecorder.
type AuditRecorderFunc func(ctx context.Context, evt DeadletterEvent) error

// RecordDeadletter implements AuditRecorder.
func (f AuditRecorderFunc) RecordDeadletter(ctx context.Context, evt DeadletterEvent) error {
	return f(ctx, evt)
}

// DeadletterEvent is the audit payload for a dead-lettered delivery. The
// fields are scoped to what the admin UI needs to display in the failure
// view: enough to debug, never the raw body or secret.
type DeadletterEvent struct {
	SubscriptionID string
	EventID        string
	EventType      string
	URL            string
	Attempts       int
	LastStatus     int
	LastError      string
	Reason         string // "schedule_exhausted" | "permanent_4xx" | "url_gone"
	OccurredAt     time.Time
}

// Errors implementing this interface carry HTTP status context. The
// classifier inspects (rather than parsing strings) when deciding
// retry vs dead-letter.
type httpStatusErr interface {
	error
	HTTPStatus() int
}

// statusError is the internal error type returned when an HTTP attempt
// completed but with a non-2xx status. It exists so the classifier can
// branch on HTTPStatus() without string parsing.
type statusError struct {
	status int
	err    error
}

func (e *statusError) Error() string  { return e.err.Error() }
func (e *statusError) Unwrap() error  { return e.err }
func (e *statusError) HTTPStatus() int { return e.status }
