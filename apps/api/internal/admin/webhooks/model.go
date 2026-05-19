package webhooks

import (
	"context"
	"errors"
	"time"
)

// Subscription is the admin-facing record of a webhook subscription.
//
// The wire shape is intentionally close to the DB schema (see
// migrations/000025_webhook_subscriptions.up.sql) — fields the admin
// UI displays map one-to-one onto JSON keys. The Secret bytes are
// never marshaled: a fresh secret is surfaced exactly once (on create
// or rotate) via the dedicated SecretReveal envelope, and never
// echoed in list/detail responses.
type Subscription struct {
	// ID is the subscription's stable identifier. UUID v7.
	ID string `json:"id"`

	// Name is the operator-facing label. Required; bounded to 200 chars
	// by the DB CHECK so the validator here doesn't need a magic number.
	Name string `json:"name"`

	// URL is the absolute endpoint the worker POSTs to.
	URL string `json:"url"`

	// Events is the subscribed event-name set ("post.published", …).
	// Empty matches nothing — admins disable a subscription by toggling
	// Active rather than emptying this slice (we keep the validation
	// strict because a "subscribes to nothing" row is almost always a
	// misconfiguration, not an intent).
	Events []string `json:"events"`

	// Active is the on/off switch the worker honors.
	Active bool `json:"active"`

	// CreatedBy is the user ID of the operator who created the
	// subscription. May be empty if the creator was later deleted
	// (SET NULL on the FK).
	CreatedBy string `json:"created_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// LastDeliveryAt is when the worker last attempted a delivery
	// for this subscription. Zero on a brand-new subscription that
	// has never seen traffic.
	LastDeliveryAt time.Time `json:"last_delivery_at,omitempty"`

	// LastDeliveryStatus is one of "success", "retry", "failed", or
	// empty when no delivery has happened. Drives the badge in the
	// admin list.
	LastDeliveryStatus string `json:"last_delivery_status,omitempty"`

	// LastDeliveryResponseCode is the HTTP code from the most recent
	// attempt, or 0 when the request never reached the subscriber.
	LastDeliveryResponseCode int `json:"last_delivery_response_code,omitempty"`

	// ConsecutiveFailures is the streak of non-success deliveries. The
	// admin list shows this so operators can spot a degrading endpoint
	// before the deadletter pipeline gives up.
	ConsecutiveFailures int `json:"consecutive_failures"`

	// DegradedAt is set when the deadletter pipeline marks the
	// subscription as degraded (retry schedule exhausted, or a
	// permanent 4xx). Zero when healthy. Surfaces as the "degraded"
	// badge in the UI; clearing it is an explicit Reset action.
	DegradedAt time.Time `json:"degraded_at,omitempty"`
}

// SubscriptionCreate is the body shape POSTed to the create endpoint.
// Distinct from Subscription so we don't accept server-only fields
// (ID, timestamps, last_*) in the input.
type SubscriptionCreate struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Active *bool    `json:"active,omitempty"`
}

// SubscriptionUpdate is the body shape PATCHed onto an existing
// subscription. Every field is optional; nil means "leave alone".
// Using pointers (rather than zero values) lets the API distinguish
// "set this to empty" from "don't touch it".
type SubscriptionUpdate struct {
	Name   *string   `json:"name,omitempty"`
	URL    *string   `json:"url,omitempty"`
	Events *[]string `json:"events,omitempty"`
	Active *bool     `json:"active,omitempty"`
}

// SubscriptionWithSecret is the envelope returned on create and on the
// rotate endpoint — exactly once, after which the raw secret is
// inaccessible. The UI must show this to the operator and instruct
// them to copy it; subsequent reads return only Subscription.
type SubscriptionWithSecret struct {
	Subscription
	// Secret is the raw HMAC key, hex-encoded so it round-trips
	// through JSON cleanly. The bytes match what the delivery worker
	// uses to sign; subscribers paste this into their config to
	// verify our signature header.
	Secret string `json:"secret"`
}

// Delivery is one row of the webhook_deliveries audit log as
// surfaced to the admin UI.
type Delivery struct {
	ID                  int64     `json:"id"`
	SubscriptionID      string    `json:"subscription_id"`
	EventID             string    `json:"event_id"`
	EventType           string    `json:"event_type"`
	Attempt             int       `json:"attempt"`
	Status              string    `json:"status"`
	ResponseCode        int       `json:"response_code"`
	DurationMs          int       `json:"duration_ms"`
	ResponseBodyPreview string    `json:"response_body_preview,omitempty"`
	Error               string    `json:"error,omitempty"`
	DeliveredAt         time.Time `json:"delivered_at"`
}

// TestResult is the body of the synchronous test-send endpoint. The
// shape mirrors what the UI shows in the test dialog: did it reach
// the subscriber, what came back, how long did it take.
type TestResult struct {
	Delivered    bool   `json:"delivered"`
	ResponseCode int    `json:"response_code"`
	DurationMs   int    `json:"duration_ms"`
	Error        string `json:"error,omitempty"`
}

// Errors returned by the Store implementation. Handler maps them to
// HTTP statuses; storage layers wrap their backend's not-found
// sentinel into ErrNotFound so the handler doesn't need to know about
// pgx.ErrNoRows.
var (
	ErrNotFound = errors.New("admin/webhooks: subscription not found")
)

// Store is the persistence contract the handlers depend on. Two
// concrete backends exist: MemoryStore for tests + dev, and (in a
// future commit, off this PR's critical path) a PostgresStore that
// writes to the migration's tables.
type Store interface {
	// Create persists a new subscription. The store generates the ID
	// (UUID v7) and timestamps; the caller supplies the rest. The
	// returned Subscription includes the generated identifier.
	Create(ctx context.Context, in SubscriptionCreate, secret []byte, createdBy string) (Subscription, error)

	// Get returns one subscription by ID. Returns ErrNotFound if no
	// row matches.
	Get(ctx context.Context, id string) (Subscription, error)

	// List returns subscriptions in created_at DESC order. Cursor is
	// opaque to the caller (in the memory backend it's the index;
	// in the Postgres backend it's the created_at|id tuple).
	List(ctx context.Context, limit int, cursor string) ([]Subscription, string, error)

	// Update applies the partial patch. Fields with nil pointers in
	// the input are left alone. Returns the updated record.
	Update(ctx context.Context, id string, in SubscriptionUpdate) (Subscription, error)

	// Delete removes the row. Cascades to webhook_deliveries via FK.
	Delete(ctx context.Context, id string) error

	// RecordDelivery appends an attempt to the audit log.
	RecordDelivery(ctx context.Context, d Delivery) error

	// ListDeliveries returns the most recent deliveries for a
	// subscription, newest first.
	ListDeliveries(ctx context.Context, subscriptionID string, limit int, cursor string) ([]Delivery, string, error)

	// Secret returns the raw HMAC bytes for a subscription. Used by
	// the test endpoint to sign the synthetic event; the wider
	// delivery worker uses a SecretResolver that wraps this call.
	Secret(ctx context.Context, id string) ([]byte, error)
}
