package invalidator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// HandlerFunc is the per-message dispatcher invoked by Subscriber for
// each pub/sub notification. The slug is the producer's plugin slug
// (or "gnf" for the fragment cache); the tag is the un-prefixed value
// the producer originally invalidated.
//
// Returning an error from a HandlerFunc is logged at Warn but does
// NOT stop the Subscriber loop: at-least-once delivery means we
// expect handlers to be idempotent, and one downstream failure
// should not freeze every other subscriber on the channel.
type HandlerFunc func(ctx context.Context, slug, tag string) error

// Subscriber consumes the Redis pub/sub channel the invalidator
// Worker publishes to, parses each message, and fans the (slug, tag)
// pair out to one or more registered HandlerFuncs.
//
// Why a separate type from Worker
//
// Worker is the producer of pub/sub messages — it drains the outbox
// and publishes. Subscriber is the consumer — it listens to the same
// channel and dispatches. The two roles run in different goroutines,
// often in different processes (the API container subscribes to
// invalidate its in-process render cache; the worker container
// publishes from the outbox). Coupling them into one struct would
// force every API replica to also wake up the outbox poller, which
// is wasted work and a real "thundering herd" hazard on Postgres.
//
// One Subscriber, many handlers
//
// A single Subscriber can dispatch to multiple handlers — the fragment
// cache, an in-process LRU, a metrics counter — without each handler
// owning its own Redis subscription. This keeps the connection count
// predictable: one PSUBSCRIBE per process regardless of how many
// caches subscribe to invalidations.
type Subscriber struct {
	rdb     *redis.Client
	channel string
	logger  *slog.Logger

	mu       sync.RWMutex
	handlers []HandlerFunc

	running atomic.Int32
}

// SubscriberOption configures a Subscriber at construction time.
type SubscriberOption func(*Subscriber)

// SubscriberWithLogger swaps the structured logger.
func SubscriberWithLogger(l *slog.Logger) SubscriberOption {
	return func(s *Subscriber) {
		if l != nil {
			s.logger = l
		}
	}
}

// SubscriberWithChannel overrides the Redis pub/sub channel name.
// Must match the Worker's channel value or the Subscriber sees
// nothing.
func SubscriberWithChannel(ch string) SubscriberOption {
	return func(s *Subscriber) {
		if ch != "" {
			s.channel = ch
		}
	}
}

// NewSubscriber constructs a Subscriber. rdb is required; passing nil
// panics for the same reason Worker's New panics on a nil pool — a
// misconfigured subscriber silently drops every invalidation, which
// is the worst possible failure mode for a cache layer.
func NewSubscriber(rdb *redis.Client, opts ...SubscriberOption) *Subscriber {
	if rdb == nil {
		panic("invalidator.NewSubscriber: redis client is required")
	}
	s := &Subscriber{
		rdb:     rdb,
		channel: DefaultChannel,
		logger:  slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handle registers a HandlerFunc. Handlers fire in registration order
// for every received message. Returns no error today, but the
// signature is fixed so a future "named handlers" extension can add
// a duplicate-name guard without changing the call site.
//
// Handlers may be registered while Run is in flight; the next
// message dispatch picks up the new handler.
func (s *Subscriber) Handle(h HandlerFunc) {
	if h == nil {
		return
	}
	s.mu.Lock()
	s.handlers = append(s.handlers, h)
	s.mu.Unlock()
}

// ErrSubscriberAlreadyRunning is returned by Run when invoked
// concurrently with itself.
var ErrSubscriberAlreadyRunning = errors.New("invalidator: subscriber already running")

// Run subscribes to the channel and dispatches every message until
// ctx is cancelled.
//
// Run blocks. The typical wiring is:
//
//	go func() { _ = sub.Run(runCtx) }()
//	// … runCtx is cancelled on shutdown …
//
// The Redis SUBSCRIBE itself is wrapped in a Receive (the go-redis
// "is subscription established" handshake) so a connection failure
// surfaces immediately rather than after the first message would
// have arrived.
func (s *Subscriber) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(0, 1) {
		return ErrSubscriberAlreadyRunning
	}
	defer s.running.Store(0)

	sub := s.rdb.Subscribe(ctx, s.channel)
	defer sub.Close()

	// Wait for the subscription to be established. Without this the
	// first messages could be dropped by go-redis' internal
	// reconnection logic.
	if _, err := sub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe %q: %w", s.channel, err)
	}

	s.logger.Info("cache invalidator subscriber started",
		slog.String("channel", s.channel))

	msgs := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("cache invalidator subscriber stopping")
			return nil
		case m, ok := <-msgs:
			if !ok {
				// go-redis closes the channel on subscription
				// teardown; treat as a clean shutdown.
				return nil
			}
			s.dispatch(ctx, m.Payload)
		}
	}
}

// dispatch parses a "<slug>:<tag>" payload and invokes every
// registered handler with the parsed parts. A malformed payload
// (no colon) is logged once and dropped — there's no useful retry
// for a structurally broken message and we don't want a poison
// message to wedge the subscriber.
func (s *Subscriber) dispatch(ctx context.Context, payload string) {
	slug, tag, ok := splitPayload(payload)
	if !ok {
		s.logger.Warn("cache invalidator: malformed pub/sub payload (no colon)",
			slog.String("payload", payload))
		return
	}

	s.mu.RLock()
	handlers := append([]HandlerFunc(nil), s.handlers...)
	s.mu.RUnlock()

	for _, h := range handlers {
		if err := h(ctx, slug, tag); err != nil {
			s.logger.Warn("cache invalidator: handler error",
				slog.String("slug", slug),
				slog.String("tag", tag),
				slog.Any("err", err))
		}
	}
}

// splitPayload parses "<slug>:<tag>" into its two parts. Tags
// themselves may contain colons (e.g. "posts:42") — we split on the
// FIRST colon only, so the slug is the leading namespace and the
// tag is everything after.
//
// Returns ok=false when the payload has no colon at all; an empty
// slug or empty tag IS allowed (the producer side is what guarantees
// neither is empty in practice; the subscriber treats them as valid
// input so a test fixture can probe edge cases without short-circuit).
func splitPayload(payload string) (slug, tag string, ok bool) {
	i := strings.IndexByte(payload, ':')
	if i < 0 {
		return "", "", false
	}
	return payload[:i], payload[i+1:], true
}
